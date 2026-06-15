// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @source: lib/foundation/persistence/postgres/frames.go
// @diverged: true
// @reason: parallel driver — SQLite dialect (positional ? params, database/sql, immediate-mode tx subsumes per-row locking) vs Postgres (pgx, $-params, explicit FOR UPDATE)

package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// ListRunningFramesNoPendingNodes state lives on rimsky_node_runs only. The pending-set
// predicate looks at in-flight run rows whose state is stale/running.
//
// unresolved-work: counts parked. A parked node_run holds its frame
// open — it is suspended work awaiting a wake (deadline-elapsed or
// external signal), not a terminal. Draining the frame to `completed`
// while a node sits parked would discard the park's eventual resume, so
// a parked row (either column, defensively) keeps the frame off the
// no-pending list until it resolves to a true terminal.
func (s *framesImpl) ListRunningFramesNoPendingNodes(ctx context.Context, tx persistence.Tx) ([]persistence.FramePending, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
        SELECT f.frame_id, f.instance_id
        FROM rimsky_frames f
        WHERE f.state = 'running'
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_node_runs r
              WHERE r.frame_id = f.frame_id
                AND (
                     (r.phase IN ('pending','active','held') AND r.state IN ('stale','running'))
                  OR r.phase = 'parked'
                  OR r.state = 'parked'
                )
          )
    `)
	if err != nil {
		return nil, fmt.Errorf("frames.ListRunningFramesNoPendingNodes: %w", err)
	}
	defer rows.Close()
	var out []persistence.FramePending
	for rows.Next() {
		var (
			frameIDStr    string
			instanceIDStr string
		)
		if err := rows.Scan(&frameIDStr, &instanceIDStr); err != nil {
			return nil, fmt.Errorf("frames.ListRunningFramesNoPendingNodes: scan: %w", err)
		}
		fid, err := uuid.Parse(frameIDStr)
		if err != nil {
			return nil, err
		}
		iid, err := uuid.Parse(instanceIDStr)
		if err != nil {
			return nil, err
		}
		out = append(out, persistence.FramePending{FrameID: fid, InstanceID: iid})
	}
	return out, rows.Err()
}

// HasFailedNode — post-stage-3: state lives on rimsky_node_runs only.
// Terminal failed rows survive past active terminal (per the stage-1
// lifecycle flip), so the predicate reads the failure flavor directly.
func (s *framesImpl) HasFailedNode(ctx context.Context, instanceID, frameID shared.UUID, tx persistence.Tx) (bool, error) {
	var anyFailed int
	err := s.q(tx).QueryRowContext(ctx, `
        -- failure-detection: not an unresolved-work predicate. Reads the
        -- failed flavor to pick the frame's terminal state; parked is
        -- irrelevant here (a parked run is neither failed nor a reason to
        -- fail the frame).
        SELECT EXISTS (
            SELECT 1 FROM rimsky_node_runs r
            JOIN rimsky_nodes n ON n.id = r.node_id
            WHERE n.instance_id = ?
              AND r.frame_id = ?
              AND r.state = 'failed'
        )
    `, instanceID.String(), frameID.String()).Scan(&anyFailed)
	if err != nil {
		return false, fmt.Errorf("frames.HasFailedNode: %w", err)
	}
	return anyFailed != 0, nil
}

func (s *framesImpl) MarkRunningFrameTerminal(
	ctx context.Context, frameID shared.UUID, finalState persistence.FrameState, tx persistence.Tx,
) (bool, error) {
	if finalState != persistence.FrameStateCompleted && finalState != persistence.FrameStateFailed {
		return false, fmt.Errorf("frames.MarkRunningFrameTerminal: invalid finalState %q", finalState)
	}
	res, err := s.q(tx).ExecContext(ctx, `
        UPDATE rimsky_frames
        SET state = ?, ended_at = ?
        WHERE frame_id = ? AND state = 'running'
    `, string(finalState), nowUTC(), frameID.String())
	if err != nil {
		return false, fmt.Errorf("frames.MarkRunningFrameTerminal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// PruneTraceForRetention deletes terminal frame ROWS (cascading their
// node_runs via the rimsky_node_runs.frame_id ON DELETE CASCADE) older
// than `cutoff` OR beyond the `recentFramesKept` most-recent terminal
// frames per instance — the lesser-of bound. Mirrors the postgres impl;
// SQLite's ROW_NUMBER() OVER PARTITION is supported natively from 3.25+
// (the modernc.org driver tracks the modern SQLite source).
//
// This replaces the prior node-run-only prune: we now delete the frame
// ROW itself and let the cascade remove its runs, so a long-lived
// durable instance's frame backlog cannot grow without bound. In-flight
// frames (queued/running, including parked-held) are exempt — only
// state IN ('completed','failed') rows are candidates, so nothing live
// is ever reaped.
//
// `recentFramesKept <= 0` disables the count bound; a zero `cutoff`
// disables the time bound. Both disabled → no-op (returns 0). When only
// one bound is active the predicate degenerates to that bound; when both
// are active a frame is reaped if EITHER predicate matches (the lesser-of
// retention — whichever keeps fewer frames).
func (s *framesImpl) PruneTraceForRetention(ctx context.Context, recentFramesKept int, cutoff time.Time) (int, error) {
	countBound := recentFramesKept > 0
	timeBound := !cutoff.IsZero()
	if !countBound && !timeBound {
		return 0, nil
	}
	// @constraint: sentinel binds let one SQL serve all three bound
	// combinations — when a bound is disabled its predicate is made
	// unsatisfiable (rk > a huge cap never matches; ended_at < zero-time
	// never matches) so the active bound(s) drive the delete.
	countCap := recentFramesKept
	if !countBound {
		// @constraint: math.MaxInt (not a 1<<62 literal) exceeds any
		// possible per-instance frame rank yet stays in range of int on
		// a 32-bit build target, where 1<<62 would overflow and fail to
		// compile; the rank predicate then never fires when the count
		// bound is disabled.
		countCap = math.MaxInt
	}
	cutoffArg := formatTime(cutoff)
	if !timeBound {
		// @constraint: RFC3339 zero time; ended_at (always > 0001 for
		// terminal frames) is never < this, so the time predicate never
		// fires.
		cutoffArg = formatTime(time.Time{})
	}
	// @constraint: standalone retention sweep — no caller-supplied tx.
	// The single DELETE is atomic on its own, so run it directly against
	// the db handle (mirroring Lineage.DeleteOlderThan); calling s.q(nil)
	// here would trip the no-nil-tx contract and panic the scheduler
	// tick. The frame→node_run ON DELETE CASCADE removes each deleted
	// frame's runs.
	res, err := (*tablesImpl)(s).db.ExecContext(ctx, `
        DELETE FROM rimsky_frames
         WHERE frame_id IN (
            SELECT frame_id FROM (
                SELECT f.frame_id, f.ended_at,
                       ROW_NUMBER() OVER (
                           PARTITION BY f.instance_id
                           ORDER BY COALESCE(f.ended_at, f.queued_at) DESC, f.frame_id DESC
                       ) AS rk
                  FROM rimsky_frames f
                 WHERE f.state IN ('completed','failed')
            ) ranked
            WHERE ranked.rk > ?
               OR (ranked.ended_at IS NOT NULL AND ranked.ended_at < ?)
         )
    `, countCap, cutoffArg)
	if err != nil {
		return 0, fmt.Errorf("frames.PruneTraceForRetention: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// MarkInstanceTerminatedIfDone applies the durable-by-default terminal
// predicate at frame-end. Idempotent set-once.
//
// Durable by default: an instance self-terminates ONLY when it was created
// with terminate_after_run = true. A durable instance (the default, false)
// is never touched here — it lives until force-terminate. This is the
// reactive instance model: an instance resolves many frames over its life
// and nothing terminates it on its own drain.
//
// Strict "run at most once more" semantics: the predicate does NOT wait for
// queued frames to drain. Because the engine calls this only at a real
// frame-end (transitionFrameEnd, after MarkRunningFrameTerminal), a
// terminate_after_run instance has by then completed exactly one frame, so
// termination is correct by construction. That another frame may be queued
// at that instant is arbitrary; the strict meaning is the useful one (see
// concept:instance). Termination reads nothing about sensors or
// publisher-subscriptions — that coupling is deliberately gone.
//
// Parked-aware guard: a defensive restatement at the instance level of the
// frame-end invariant (parity with ListRunningFramesNoPendingNodes). A
// parked node_run is suspended work awaiting a wake, not a terminal, so it
// blocks termination — a later wake must never land on a terminated
// instance. We protect this property even though, at a real frame-end, no
// parked run can be present (a parked run holds the frame open, so the
// frame would not have ended): the guard makes termination impossible while
// any node_run is unresolved (stale, running, or parked) regardless of how
// this is invoked.
func (s *framesImpl) MarkInstanceTerminatedIfDone(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx, `
        UPDATE rimsky_instances
        SET terminated_at = ?
        WHERE id = ?
          AND terminated_at IS NULL
          AND terminate_after_run = 1
          AND NOT EXISTS (
              -- unresolved-work: counts parked. A parked run is suspended
              -- work awaiting a wake, not a terminal, so it blocks instance
              -- termination (a later wake must not land on a terminated
              -- instance). Defensive restatement of the frame-end invariant.
              SELECT 1 FROM rimsky_node_runs r
              JOIN rimsky_nodes n ON n.id = r.node_id
              WHERE n.instance_id = rimsky_instances.id
                AND (
                     (r.phase IN ('pending','active','held') AND r.state IN ('stale','running'))
                  OR r.phase = 'parked'
                  OR r.state = 'parked'
                )
          )
    `, nowUTC(), instanceID.String())
	if err != nil {
		return fmt.Errorf("frames.MarkInstanceTerminatedIfDone: %w", err)
	}
	return nil
}

// ListQueuedFramesReadyToStart returns at most one queued frame per
// instance — the oldest queued — for instances that have no
// currently-running frame AND are not terminated.
//
// Terminated-instance guard: under strict terminal semantics
// (concept:instance), a terminate_after_run instance can reach terminal at
// frame-end while a frame it never ran is still `queued` (a message arrived
// mid-run). That orphaned queued frame must never be promoted — promoting
// it would run work against a terminated instance. The frame row is cleaned
// up by the instance's eventual delete (cascade) and by trace retention; it
// simply never runs. We join rimsky_instances and require terminated_at IS
// NULL to exclude it.
//
// SQLite has no DISTINCT ON; we emulate via row_number() OVER (PARTITION BY).
func (s *framesImpl) ListQueuedFramesReadyToStart(ctx context.Context, tx persistence.Tx) ([]persistence.FrameQueuedReady, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
        WITH ranked AS (
            SELECT f.frame_id, f.instance_id, f.source_node_ids,
                   ROW_NUMBER() OVER (PARTITION BY f.instance_id ORDER BY f.queued_at ASC) AS rn
            FROM rimsky_frames f
            JOIN rimsky_instances i ON i.id = f.instance_id
            WHERE f.state = 'queued'
              AND i.terminated_at IS NULL
              AND NOT EXISTS (
                  SELECT 1 FROM rimsky_frames r
                  WHERE r.instance_id = f.instance_id AND r.state = 'running'
              )
        )
        SELECT frame_id, instance_id, source_node_ids
        FROM ranked
        WHERE rn = 1
    `)
	if err != nil {
		return nil, fmt.Errorf("frames.ListQueuedFramesReadyToStart: %w", err)
	}
	defer rows.Close()
	var out []persistence.FrameQueuedReady
	for rows.Next() {
		var (
			frameIDStr     string
			instanceIDStr  string
			sourceNodeJSON string
		)
		if err := rows.Scan(&frameIDStr, &instanceIDStr, &sourceNodeJSON); err != nil {
			return nil, fmt.Errorf("frames.ListQueuedFramesReadyToStart: scan: %w", err)
		}
		fid, err := uuid.Parse(frameIDStr)
		if err != nil {
			return nil, err
		}
		iid, err := uuid.Parse(instanceIDStr)
		if err != nil {
			return nil, err
		}
		sources, err := unmarshalUUIDArray(sourceNodeJSON)
		if err != nil {
			return nil, fmt.Errorf("frames.ListQueuedFramesReadyToStart: source_node_ids: %w", err)
		}
		out = append(out, persistence.FrameQueuedReady{FrameID: fid, InstanceID: iid, SourceNodeIDs: sources})
	}
	return out, rows.Err()
}

func (s *framesImpl) PromoteQueuedFrameToRunning(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (bool, error) {
	res, err := s.q(tx).ExecContext(ctx, `
        UPDATE rimsky_frames
        SET state = 'running', started_at = ?
        WHERE frame_id = ? AND state = 'queued'
    `, nowUTC(), frameID.String())
	if err != nil {
		return false, fmt.Errorf("frames.PromoteQueuedFrameToRunning: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// GetRunningFrameID returns the frame_id of the instance's currently-
// running frame, or (nil, nil) when none is running. See the postgres
// mirror for rationale; the ORDER BY started_at DESC LIMIT 1 is the same
// defensive single-row tiebreak.
func (s *framesImpl) GetRunningFrameID(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (*shared.UUID, error) {
	var frameIDStr string
	err := s.q(tx).QueryRowContext(ctx, `
        SELECT frame_id
          FROM rimsky_frames
         WHERE instance_id = ? AND state = 'running'
         ORDER BY started_at DESC
         LIMIT 1
    `, instanceID.String()).Scan(&frameIDStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("frames.GetRunningFrameID: %w", err)
	}
	fid, err := uuid.Parse(frameIDStr)
	if err != nil {
		return nil, fmt.Errorf("frames.GetRunningFrameID: parse frame_id: %w", err)
	}
	return &fid, nil
}

// MarkSourceNodeStale — post-stage-3 cutover. See postgres mirror for
// rationale. Binds rimsky_nodes.frame_id then INSERTs a fresh pending
// stale run row when no in-flight row exists; falls back to the
// "already in-flight pending stale row for this frame" predicate for
// under-contention re-entry.
func (s *framesImpl) MarkSourceNodeStale(
	ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx persistence.Tx,
) (bool, error) {
	if _, err := s.q(tx).ExecContext(ctx, `
        UPDATE rimsky_nodes
        SET frame_id = ?, updated_at = ?
        WHERE instance_id = ? AND id = ?
    `, frameID.String(), nowUTC(), instanceID.String(), nodeID.String()); err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: bind frame: %w", err)
	}
	// @constraint: populate required_stores from the template node-def
	// via a JSON lookup; see postgres mirror for rationale.
	//
	// @concept: run-scope — under RunScope-first the new row lives in
	// the instance's main RunScope (the only RunScope a frame source's
	// run can belong to).
	res, err := s.q(tx).ExecContext(ctx, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        SELECT ?, n.id, n.executor,
               COALESCE((
                 SELECT json_group_array(json_extract(store.value, '$.name'))
                   FROM rimsky_instances i
                   JOIN rimsky_templates t ON t.id = i.template_hash
                   JOIN json_each(t.spec, '$.nodes') AS nd
                   JOIN json_each(nd.value, '$.stores') AS store
                  WHERE i.id = n.instance_id
                    AND json_extract(nd.value, '$.type') = n.node_type
               ), '[]'),
               ?, 'pending', 'stale', ?, inst.main_run_scope_id
          FROM rimsky_nodes n
          JOIN rimsky_instances inst ON inst.id = n.instance_id
         WHERE n.id = ?
           AND n.instance_id = ?
           AND NOT EXISTS (
             -- in-flight guard: counts parked. A node with any in-flight
             -- run (including parked) must not get a second stale run row;
             -- the partial unique index enforces one in-flight row per
             -- node, so parked belongs in this set.
             SELECT 1 FROM rimsky_node_runs r
              WHERE r.node_id = ?
                AND r.phase IN ('pending','active','held','parked')
           )
    `, uuid.New().String(), nowUTC(), frameID.String(), nodeID.String(), instanceID.String(), nodeID.String())
	if err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: insert run row: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 1 {
		return true, nil
	}
	var anyMatched int
	if err := s.q(tx).QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM rimsky_node_runs r
             WHERE r.node_id = ?
               AND r.phase = 'pending'
               AND r.state = 'stale'
               AND r.frame_id = ?
        )
    `, nodeID.String(), frameID.String()).Scan(&anyMatched); err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: existence check: %w", err)
	}
	return anyMatched != 0, nil
}

// ListStuckRunningFrames returns running frames past their timeout with
// no claimed dispatches and at least one stale/running node. SQLite has
// no `interval` arithmetic — we compute (started_at + timeout) in Go and
// filter in app code.
//
// To avoid pulling every running frame, we filter the SQL by state and
// existence predicates, then do the time math in Go.
func (s *framesImpl) ListStuckRunningFrames(ctx context.Context, tx persistence.Tx) ([]persistence.FrameStuck, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
        SELECT f.frame_id, f.instance_id, f.frame_timeout_ms, f.last_progress_at
        FROM rimsky_frames f
        WHERE f.state = 'running'
          AND f.last_progress_at IS NOT NULL
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_node_runs d
              WHERE d.frame_id = f.frame_id AND d.claimed_by IS NOT NULL
          )
          AND EXISTS (
              -- dispatch-eligible: excludes parked. A parked run is
              -- intentionally suspended awaiting a wake, not stuck — the
              -- no-progress soft warning fires only when there is
              -- dispatchable work that has stalled, so parked rows are
              -- deliberately omitted here.
              SELECT 1 FROM rimsky_node_runs r
              JOIN rimsky_nodes n ON n.id = r.node_id
              WHERE n.instance_id = f.instance_id
                AND r.phase IN ('pending','active','held')
                AND r.state IN ('stale','running')
          )
    `)
	if err != nil {
		return nil, fmt.Errorf("frames.ListStuckRunningFrames: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	var out []persistence.FrameStuck
	for rows.Next() {
		var (
			frameIDStr      string
			instanceIDStr   string
			frameTimeoutMs  int64
			lastProgressStr string
		)
		if err := rows.Scan(&frameIDStr, &instanceIDStr, &frameTimeoutMs, &lastProgressStr); err != nil {
			return nil, fmt.Errorf("frames.ListStuckRunningFrames: scan: %w", err)
		}
		lastProgress, err := parseTime(lastProgressStr)
		if err != nil {
			return nil, err
		}
		// @constraint: frame_timeout_ms measures "no progress in window"
		// rather than frame age — compare against last_progress_at
		// (refreshed by every node-state transition write) instead of
		// started_at, so a progressing self-invalidate loop does not
		// trip the soft warning even if its total runtime exceeds the
		// timeout.
		if !lastProgress.Add(time.Duration(frameTimeoutMs) * time.Millisecond).Before(now) {
			continue
		}
		fid, err := uuid.Parse(frameIDStr)
		if err != nil {
			return nil, err
		}
		iid, err := uuid.Parse(instanceIDStr)
		if err != nil {
			return nil, err
		}
		out = append(out, persistence.FrameStuck{FrameID: fid, InstanceID: iid, FrameTimeoutMs: frameTimeoutMs})
	}
	return out, rows.Err()
}

func (s *framesImpl) ListOrphanFrameDispatches(ctx context.Context, tx persistence.Tx) ([]persistence.OrphanFrameDispatch, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
        SELECT d.id, d.claimed_by, d.frame_id
        FROM rimsky_node_runs d
        JOIN rimsky_frames f ON f.frame_id = d.frame_id
        WHERE d.claimed_by IS NOT NULL
          AND f.state IN ('completed','failed')
    `)
	if err != nil {
		return nil, fmt.Errorf("frames.ListOrphanFrameDispatches: %w", err)
	}
	defer rows.Close()
	var out []persistence.OrphanFrameDispatch
	for rows.Next() {
		var (
			dispatchIDStr string
			claimedBy     string
			frameIDStr    string
		)
		if err := rows.Scan(&dispatchIDStr, &claimedBy, &frameIDStr); err != nil {
			return nil, fmt.Errorf("frames.ListOrphanFrameDispatches: scan: %w", err)
		}
		did, err := uuid.Parse(dispatchIDStr)
		if err != nil {
			return nil, err
		}
		fid, err := uuid.Parse(frameIDStr)
		if err != nil {
			return nil, err
		}
		out = append(out, persistence.OrphanFrameDispatch{DispatchID: did, ClaimedBy: claimedBy, FrameID: fid})
	}
	return out, rows.Err()
}

// LookupFrameResolutionMode reads (frame_resolution_mode, frame_timeout_ms) from the
// instance's template spec via json_extract.
func (s *framesImpl) LookupFrameResolutionMode(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (persistence.FrameResolutionMode, int64, error) {
	var (
		mode           sql.NullString
		frameTimeoutMs sql.NullInt64
	)
	err := s.q(tx).QueryRowContext(ctx, `
        SELECT json_extract(t.spec, '$.frame_resolution_mode') AS mode,
               json_extract(t.spec, '$.frame_timeout_ms') AS frame_timeout_ms
        FROM rimsky_instances i
        JOIN rimsky_templates  t ON t.id = i.template_hash
        WHERE i.id = ?
    `, instanceID.String()).Scan(&mode, &frameTimeoutMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, fmt.Errorf("frames.LookupFrameResolutionMode: instance %s not found", instanceID)
		}
		return "", 0, fmt.Errorf("frames.LookupFrameResolutionMode: %w", err)
	}
	timeout := int64(600000)
	if frameTimeoutMs.Valid && frameTimeoutMs.Int64 > 0 {
		timeout = frameTimeoutMs.Int64
	}
	return persistence.FrameResolutionMode(mode.String), timeout, nil
}

func (s *framesImpl) EnqueueSerialFrame(
	ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx persistence.Tx,
) (shared.UUID, error) {
	frameID := uuid.New()
	now := nowUTC()
	// @constraint: explicitly write last_progress_at at insert time
	// using nowUTC() (the fixed-width timeLayoutFixedNanos layout, whose
	// lexicographic order matches chronological order) so the column is
	// uniformly nano-precision across all rows. The migration's strftime
	// DEFAULT only delivers millisecond precision; relying on it for
	// runtime inserts would leave the column with mixed precision and
	// break any future SQL-level string comparison against the column.
	_, err := s.q(tx).ExecContext(ctx, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, frame_timeout_ms, last_progress_at)
        VALUES (?, ?, 'serial_queue', 'queued', ?, ?, ?, ?)
    `, frameID.String(), instanceID.String(),
		marshalUUIDArray([]shared.UUID{sourceNodeID}), now, frameTimeoutMs, now)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("frames.EnqueueSerialFrame: %w", err)
	}
	return frameID, nil
}

// EnqueueCoalesceFrame inserts a queued coalesce frame, or appends the
// source node to an existing pending coalesce row for the instance.
// Reads-then-updates inside the caller's tx (BEGIN IMMEDIATE on
// SQLite gives writer-slot serialisation for the duration).
func (s *framesImpl) EnqueueCoalesceFrame(
	ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx persistence.Tx,
) (shared.UUID, error) {
	// existing for an existing queued+coalesce frame for this instance under
	// the surrounding tx.
	var existing string
	err := s.q(tx).QueryRowContext(ctx, `
        SELECT frame_id FROM rimsky_frames
        WHERE instance_id = ? AND state = 'queued' AND frame_resolution_mode = 'coalesce'
        LIMIT 1
    `, instanceID.String()).Scan(&existing)
	if err == nil {
		fid, perr := uuid.Parse(existing)
		if perr != nil {
			return shared.UUID{}, perr
		}
		var existingJSON string
		if err := s.q(tx).QueryRowContext(ctx,
			`SELECT source_node_ids FROM rimsky_frames WHERE frame_id = ?`, existing,
		).Scan(&existingJSON); err != nil {
			return shared.UUID{}, fmt.Errorf("frames.EnqueueCoalesceFrame: re-read: %w", err)
		}
		ids, err := unmarshalUUIDArray(existingJSON)
		if err != nil {
			return shared.UUID{}, err
		}
		hasIt := false
		for _, id := range ids {
			if id == sourceNodeID {
				hasIt = true
				break
			}
		}
		if !hasIt {
			ids = append(ids, sourceNodeID)
			if _, err := s.q(tx).ExecContext(ctx,
				`UPDATE rimsky_frames SET source_node_ids = ? WHERE frame_id = ?`,
				marshalUUIDArray(ids), existing,
			); err != nil {
				return shared.UUID{}, fmt.Errorf("frames.EnqueueCoalesceFrame: append: %w", err)
			}
		}
		return fid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return shared.UUID{}, fmt.Errorf("frames.EnqueueCoalesceFrame: select: %w", err)
	}

	// @constraint: insert a new coalesce frame and write
	// last_progress_at explicitly (see EnqueueSerialFrame for the
	// precision rationale).
	frameID := uuid.New()
	now := nowUTC()
	if _, err := s.q(tx).ExecContext(ctx, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, frame_timeout_ms, last_progress_at)
        VALUES (?, ?, 'coalesce', 'queued', ?, ?, ?, ?)
    `, frameID.String(), instanceID.String(),
		marshalUUIDArray([]shared.UUID{sourceNodeID}), now, frameTimeoutMs, now); err != nil {
		return shared.UUID{}, fmt.Errorf("frames.EnqueueCoalesceFrame: insert: %w", err)
	}
	return frameID, nil
}

// ListRunningFramesWithSources returns running frames along with their
// source_node_ids. Used by the runtime upstream-refresh pre-stage sweep.
func (s *framesImpl) ListRunningFramesWithSources(
	ctx context.Context, tx persistence.Tx,
) ([]persistence.FrameRunningSources, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
        SELECT frame_id, instance_id, source_node_ids
          FROM rimsky_frames
         WHERE state = 'running'
    `)
	if err != nil {
		return nil, fmt.Errorf("frames.ListRunningFramesWithSources: %w", err)
	}
	defer rows.Close()
	var out []persistence.FrameRunningSources
	for rows.Next() {
		var (
			frameID, instanceID, idsJSON string
		)
		if err := rows.Scan(&frameID, &instanceID, &idsJSON); err != nil {
			return nil, fmt.Errorf("frames.ListRunningFramesWithSources: scan: %w", err)
		}
		fid, err := uuid.Parse(frameID)
		if err != nil {
			return nil, fmt.Errorf("frames.ListRunningFramesWithSources: parse frame_id: %w", err)
		}
		iid, err := uuid.Parse(instanceID)
		if err != nil {
			return nil, fmt.Errorf("frames.ListRunningFramesWithSources: parse instance_id: %w", err)
		}
		ids, err := unmarshalUUIDArray(idsJSON)
		if err != nil {
			return nil, fmt.Errorf("frames.ListRunningFramesWithSources: unmarshal sources: %w", err)
		}
		out = append(out, persistence.FrameRunningSources{
			FrameID: fid, InstanceID: iid, SourceNodeIDs: ids,
		})
	}
	return out, nil
}

// AppendSourceToQueuedFrame appends a node id to a queued frame's
// source_node_ids JSON array (idempotent — no-op if the id is already
// present). The WHERE clause restricts the read+update to queued
// frames only, so a frame that has since been promoted to running or
// marked terminal silently no-ops. Used by invalidateNextFrame to
// attach upstream-refresh upstreams to the same frame as the receiver.
func (s *framesImpl) AppendSourceToQueuedFrame(
	ctx context.Context, frameID, nodeID shared.UUID, tx persistence.Tx,
) error {
	var existingJSON string
	err := s.q(tx).QueryRowContext(ctx, `
        SELECT source_node_ids FROM rimsky_frames
         WHERE frame_id = ? AND state = 'queued'
    `, frameID.String()).Scan(&existingJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // not queued anymore (or never existed) — silent no-op
	}
	if err != nil {
		return fmt.Errorf("frames.AppendSourceToQueuedFrame: select: %w", err)
	}
	ids, err := unmarshalUUIDArray(existingJSON)
	if err != nil {
		return fmt.Errorf("frames.AppendSourceToQueuedFrame: unmarshal: %w", err)
	}
	for _, id := range ids {
		if id == nodeID {
			return nil // already present — idempotent no-op
		}
	}
	ids = append(ids, nodeID)
	if _, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_frames SET source_node_ids = ? WHERE frame_id = ? AND state = 'queued'`,
		marshalUUIDArray(ids), frameID.String(),
	); err != nil {
		return fmt.Errorf("frames.AppendSourceToQueuedFrame: update: %w", err)
	}
	return nil
}

// ListForObservability returns frames matching filter for the
// observability /v1/observability/frames endpoint. Cursor pagination
// over (queued_at DESC, frame_id DESC).
func (s *framesImpl) ListForObservability(ctx context.Context, filter persistence.FrameListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.FrameRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var instArg, stateArg any
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.State != "" {
		stateArg = string(filter.State)
	}
	var cursorQAt, cursorFid any
	if pag.Cursor != "" {
		q, fid, err := decodeFrameCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, fmt.Errorf("frames.list: bad cursor: %w", err)
		}
		cursorQAt = formatTime(q)
		cursorFid = fid.String()
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT frame_id, instance_id, state, frame_resolution_mode, started_at, ended_at, frame_timeout_ms, queued_at
		   FROM rimsky_frames
		  WHERE (? IS NULL OR instance_id = ?)
		    AND (? IS NULL OR state = ?)
		    AND (? IS NULL OR (queued_at, frame_id) < (?, ?))
		  ORDER BY queued_at DESC, frame_id DESC
		  LIMIT ?`,
		instArg, instArg, stateArg, stateArg, cursorQAt, cursorQAt, cursorFid, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.FrameRow]{}, err
	}
	defer rows.Close()
	var out []persistence.FrameRow
	var lastQAt time.Time
	for rows.Next() {
		var (
			r          persistence.FrameRow
			frameID    string
			instanceID string
			state      string
			mode       string
			startedAt  sql.NullString
			endedAt    sql.NullString
			queuedAt   string
		)
		if err := rows.Scan(&frameID, &instanceID, &state, &mode, &startedAt, &endedAt, &r.FrameTimeoutMs, &queuedAt); err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		fid, err := uuid.Parse(frameID)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		iid, err := uuid.Parse(instanceID)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		r.FrameID = fid
		r.InstanceID = iid
		r.State = persistence.FrameState(state)
		r.Mode = persistence.FrameResolutionMode(mode)
		if startedAt.Valid {
			t, err := parseTime(startedAt.String)
			if err == nil {
				r.StartedAt = &t
			}
		}
		if endedAt.Valid {
			t, err := parseTime(endedAt.String)
			if err == nil {
				r.EndedAt = &t
			}
		}
		if t, err := parseTime(queuedAt); err == nil {
			lastQAt = t
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.FrameRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = encodeFrameCursor(lastQAt, out[len(out)-1].FrameID)
	}
	return persistence.PaginatedListResult[persistence.FrameRow]{Rows: out, NextCursor: nextCursor}, nil
}

type frameCursor struct {
	Q time.Time   `json:"q"`
	F shared.UUID `json:"f"`
}

func encodeFrameCursor(queued time.Time, fid shared.UUID) string {
	b, _ := json.Marshal(frameCursor{Q: queued, F: fid})
	return base64.StdEncoding.EncodeToString(b)
}

func decodeFrameCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c frameCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.Q, c.F, nil
}

// RefreshProgress updates rimsky_frames.last_progress_at to now() for
// the given frame. Called by the node-state-transition write path on
// every UpdateState that carries the frame's id, so frame_timeout_ms
// measures no-progress-in-window rather than frame age.
//
// See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §7.
func (s *framesImpl) RefreshProgress(ctx context.Context, frameID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_frames SET last_progress_at = ? WHERE frame_id = ?`,
		nowUTC(), frameID.String(),
	)
	if err != nil {
		return fmt.Errorf("frames.RefreshProgress: %w", err)
	}
	return nil
}

// CountHeldFrames returns the number of running frames that have at
// least one parked rimsky_node_runs row attached via frame_id.
//
// unresolved-work: counts parked (by definition — this query exists to
// count frames a parked run is holding open; consistent with
// ListRunningFramesNoPendingNodes treating parked as unresolved).
func (s *framesImpl) CountHeldFrames(ctx context.Context, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT f.frame_id)
		   FROM rimsky_frames f
		   JOIN rimsky_node_runs d ON d.frame_id = f.frame_id
		  WHERE f.state = 'running' AND d.phase = 'parked'`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("frames.CountHeldFrames: %w", err)
	}
	return n, nil
}

// GetForObservability returns one frame by id.
func (s *framesImpl) GetForObservability(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRow, error) {
	var (
		r         persistence.FrameRow
		fidStr    string
		iidStr    string
		state     string
		mode      string
		startedAt sql.NullString
		endedAt   sql.NullString
	)
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT frame_id, instance_id, state, frame_resolution_mode, started_at, ended_at, frame_timeout_ms
		   FROM rimsky_frames WHERE frame_id = ?`,
		frameID.String(),
	).Scan(&fidStr, &iidStr, &state, &mode, &startedAt, &endedAt, &r.FrameTimeoutMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	fid, err := uuid.Parse(fidStr)
	if err != nil {
		return nil, err
	}
	iid, err := uuid.Parse(iidStr)
	if err != nil {
		return nil, err
	}
	r.FrameID = fid
	r.InstanceID = iid
	r.State = persistence.FrameState(state)
	r.Mode = persistence.FrameResolutionMode(mode)
	if startedAt.Valid {
		t, err := parseTime(startedAt.String)
		if err == nil {
			r.StartedAt = &t
		}
	}
	if endedAt.Valid {
		t, err := parseTime(endedAt.String)
		if err == nil {
			r.EndedAt = &t
		}
	}
	return &r, nil
}
