// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// ListRunningFramesNoPendingNodes returns running frames whose nodes in
// the same (instance_id, frame_id) scope have no unresolved run row.
//
// Post-stage-3: state lives on rimsky_node_runs only.
//
// unresolved-work: counts parked. A parked node_run holds its frame open
// — it is suspended work awaiting a wake (deadline-elapsed or external
// signal), not a terminal. Draining the frame to `completed` while a node
// sits parked would discard the park's eventual resume, so a parked row
// (either column, defensively) keeps the frame off the no-pending list
// until it resolves to a true terminal. `failed` is genuinely terminal
// and does not hold the frame.
func (s *framesImpl) ListRunningFramesNoPendingNodes(ctx context.Context, tx persistence.Tx) ([]persistence.FramePending, error) {
	rows, err := s.q(tx).Query(ctx, `
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
		var p persistence.FramePending
		if err := rows.Scan(&p.FrameID, &p.InstanceID); err != nil {
			return nil, fmt.Errorf("frames.ListRunningFramesNoPendingNodes: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HasFailedNode returns true when any run row for the given
// (instanceID, frameID) reached state='failed'.
//
// Post-stage-3: state lives on rimsky_node_runs only; terminal failed
// rows survive past active terminal (per the stage-1 lifecycle flip)
// so this query reads the failure flavor directly without back-joining
// through rimsky_nodes.
func (s *framesImpl) HasFailedNode(ctx context.Context, instanceID, frameID shared.UUID, tx persistence.Tx) (bool, error) {
	var anyFailed bool
	err := s.q(tx).QueryRow(ctx, `
        -- failure-detection: not an unresolved-work predicate. Reads the
        -- failed flavor to pick the frame's terminal state; parked is
        -- irrelevant here (a parked run is neither failed nor a reason to
        -- fail the frame).
        SELECT EXISTS (
            SELECT 1 FROM rimsky_node_runs r
            JOIN rimsky_nodes n ON n.id = r.node_id
            WHERE n.instance_id = $1
              AND r.frame_id = $2
              AND r.state = 'failed'
        )
    `, instanceID, frameID).Scan(&anyFailed)
	if err != nil {
		return false, fmt.Errorf("frames.HasFailedNode: %w", err)
	}
	return anyFailed, nil
}

// MarkRunningFrameTerminal flips a running frame to its terminal state
// and stamps ended_at=now(). Returns transitioned=true when exactly one
// row moved.
func (s *framesImpl) MarkRunningFrameTerminal(
	ctx context.Context, frameID shared.UUID, finalState persistence.FrameState, tx persistence.Tx,
) (bool, error) {
	if finalState != persistence.FrameStateCompleted && finalState != persistence.FrameStateFailed {
		return false, fmt.Errorf("frames.MarkRunningFrameTerminal: invalid finalState %q", finalState)
	}
	cmd, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_frames
        SET state = $1, ended_at = now()
        WHERE frame_id = $2 AND state = 'running'
    `, string(finalState), frameID)
	if err != nil {
		return false, fmt.Errorf("frames.MarkRunningFrameTerminal: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

// MarkInstanceTerminatedIfDone applies the durable-by-default terminal
// predicate at frame-end, setting rimsky_instances.terminated_at=now()
// when it holds. Idempotent set-once.
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
	_, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_instances i
        SET terminated_at = now()
        WHERE i.id = $1
          AND i.terminated_at IS NULL
          AND i.terminate_after_run = true
          AND NOT EXISTS (
              -- unresolved-work: counts parked. A parked run is suspended
              -- work awaiting a wake, not a terminal, so it blocks instance
              -- termination (a later wake must not land on a terminated
              -- instance). Defensive restatement of the frame-end invariant.
              SELECT 1 FROM rimsky_node_runs r
              JOIN rimsky_nodes n ON n.id = r.node_id
              WHERE n.instance_id = i.id
                AND (
                     (r.phase IN ('pending','active','held') AND r.state IN ('stale','running'))
                  OR r.phase = 'parked'
                  OR r.state = 'parked'
                )
          )
    `, instanceID)
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
func (s *framesImpl) ListQueuedFramesReadyToStart(ctx context.Context, tx persistence.Tx) ([]persistence.FrameQueuedReady, error) {
	rows, err := s.q(tx).Query(ctx, `
        SELECT DISTINCT ON (f.instance_id)
            f.frame_id, f.instance_id, f.source_node_ids
        FROM rimsky_frames f
        JOIN rimsky_instances i ON i.id = f.instance_id
        WHERE f.state = 'queued'
          AND i.terminated_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_frames r
              WHERE r.instance_id = f.instance_id AND r.state = 'running'
          )
        ORDER BY f.instance_id, f.queued_at ASC
    `)
	if err != nil {
		return nil, fmt.Errorf("frames.ListQueuedFramesReadyToStart: %w", err)
	}
	defer rows.Close()
	var out []persistence.FrameQueuedReady
	for rows.Next() {
		var r persistence.FrameQueuedReady
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &r.SourceNodeIDs); err != nil {
			return nil, fmt.Errorf("frames.ListQueuedFramesReadyToStart: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PromoteQueuedFrameToRunning flips a queued frame to running and
// stamps started_at=now(). Returns transitioned=true on success.
func (s *framesImpl) PromoteQueuedFrameToRunning(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (bool, error) {
	cmd, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_frames
        SET state = 'running', started_at = now()
        WHERE frame_id = $1 AND state = 'queued'
    `, frameID)
	if err != nil {
		return false, fmt.Errorf("frames.PromoteQueuedFrameToRunning: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

// GetRunningFrameID returns the frame_id of the instance's currently-
// running frame, or (nil, nil) when none is running. Under the
// serial-queue model at most one running frame exists per instance; the
// ORDER BY started_at DESC LIMIT 1 is a defensive tiebreak that picks the
// most-recently-started row should two ever coexist (they must not).
func (s *framesImpl) GetRunningFrameID(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (*shared.UUID, error) {
	var frameID shared.UUID
	err := s.q(tx).QueryRow(ctx, `
        SELECT frame_id
          FROM rimsky_frames
         WHERE instance_id = $1 AND state = 'running'
         ORDER BY started_at DESC
         LIMIT 1
    `, instanceID).Scan(&frameID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("frames.GetRunningFrameID: %w", err)
	}
	return &frameID, nil
}

// MarkSourceNodeStale binds a frame's source node to the frame. Post-
// stage-3 cutover: state lives on rimsky_node_runs only, so this helper:
//   - binds rimsky_nodes.frame_id = $1 (idempotent re-bind for sources
//     already pinned to another in-flight frame is gated by the
//     in-bounds predicate below),
//   - INSERTs a fresh pending run row with state='stale' + frame_id=$1
//     when no in-flight row exists (the source was 'fresh', 'failed',
//     or completed since the last frame).
//
// In-bounds: the partial unique index uq_node_runs_in_flight_per_node
// guarantees at most one in-flight row per node. A source that's
// already in-flight (e.g., a redelivered frame-start under contention)
// produces zero rows inserted; the caller's `matched=true` predicate
// must therefore include the "already in-flight stale row for this
// frame" case so the engine doesn't roll back the promotion.
//
// Returns matched=true when:
//   - the INSERT moved one row (the typical fresh→stale source path),
//   - OR an in-flight stale run row already exists for this node+frame
//     (under-contention re-entry; safe to treat as a no-op success).
//
// Out-of-bounds (matched=false) covers: source already in a different
// frame's in-flight run row, or running, or parked.
func (s *framesImpl) MarkSourceNodeStale(
	ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx persistence.Tx,
) (bool, error) {
	// @deliberate: unconditional frame_id rebind — idempotent so the
	// under-contention re-entry path (caller retrying after a transient
	// failure) succeeds without a pre-check.
	if _, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_nodes
        SET frame_id = $1, updated_at = now()
        WHERE instance_id = $2 AND id = $3
    `, frameID, instanceID, nodeID); err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: bind frame: %w", err)
	}
	// @constraint: required_stores must be populated from the template
	// node-def so the supervisor's SelectCandidates pool-predicate
	// (required_stores ⊆ accepted_stores) routes the row correctly.
	// @concept: run-scope
	// @deliberate: frame source's run row lives in the instance's main
	// RunScope (the only RunScope a frame source's run can belong to —
	// sub-graph + fan-out children allocate via AffirmNodeRunRow /
	// DispatchChildren, not via the frame source path).
	tag, err := s.q(tx).Exec(ctx, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        SELECT gen_random_uuid(), n.id, n.executor,
               COALESCE((
                 SELECT array_agg(store->>'name')
                   FROM rimsky_instances i
                   JOIN rimsky_templates t ON t.id = i.template_hash
                   CROSS JOIN LATERAL jsonb_array_elements(t.spec->'nodes') AS nd
                   LEFT JOIN LATERAL jsonb_array_elements(nd->'stores') AS store ON true
                  WHERE i.id = n.instance_id
                    AND nd->>'type' = n.node_type
                    AND store IS NOT NULL
               ), ARRAY[]::text[]) AS required_stores,
               NOW(), 'pending', 'stale', $1, inst.main_run_scope_id
          FROM rimsky_nodes n
          JOIN rimsky_instances inst ON inst.id = n.instance_id
         WHERE n.id = $2
           AND n.instance_id = $3
           AND NOT EXISTS (
             -- in-flight guard: counts parked. A node with any in-flight
             -- run (including parked) must not get a second stale run row;
             -- the partial unique index enforces one in-flight row per
             -- node, so parked belongs in this set.
             SELECT 1 FROM rimsky_node_runs r
              WHERE r.node_id = $2
                AND r.phase IN ('pending','active','held','parked')
           )
    `, frameID, nodeID, instanceID)
	if err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: insert run row: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	// @deliberate: zero RowsAffected falls back to an existence check for
	// an already-in-flight stale row pinned to this frame, so the
	// under-contention re-entry returns matched=true rather than failing.
	var anyMatched bool
	if err := s.q(tx).QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM rimsky_node_runs r
             WHERE r.node_id = $1
               AND r.phase = 'pending'
               AND r.state = 'stale'
               AND r.frame_id = $2
        )
    `, nodeID, frameID).Scan(&anyMatched); err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: existence check: %w", err)
	}
	return anyMatched, nil
}

// ListStuckRunningFrames returns running frames where last_progress_at
// is older than frame_timeout_ms (i.e. no node-state transition has
// happened in the timeout window), with no claimed dispatches and at
// least one stale/running node.
//
// Per the reactive-loops + lifecycle-handlers spec §7, frame_timeout_ms
// measures "no progress in window" rather than frame age — a
// progressing self-invalidate loop should not trip the soft warning
// even if its total runtime exceeds the timeout.
func (s *framesImpl) ListStuckRunningFrames(ctx context.Context, tx persistence.Tx) ([]persistence.FrameStuck, error) {
	rows, err := s.q(tx).Query(ctx, `
        SELECT f.frame_id, f.instance_id, f.frame_timeout_ms
        FROM rimsky_frames f
        WHERE f.state = 'running'
          AND f.last_progress_at + (f.frame_timeout_ms || ' milliseconds')::interval < now()
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
	var out []persistence.FrameStuck
	for rows.Next() {
		var r persistence.FrameStuck
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &r.FrameTimeoutMs); err != nil {
			return nil, fmt.Errorf("frames.ListStuckRunningFrames: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListOrphanFrameDispatches returns dispatch rows whose claim is non-NULL
// but whose owning frame has reached terminal state.
func (s *framesImpl) ListOrphanFrameDispatches(ctx context.Context, tx persistence.Tx) ([]persistence.OrphanFrameDispatch, error) {
	rows, err := s.q(tx).Query(ctx, `
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
		var r persistence.OrphanFrameDispatch
		if err := rows.Scan(&r.DispatchID, &r.ClaimedBy, &r.FrameID); err != nil {
			return nil, fmt.Errorf("frames.ListOrphanFrameDispatches: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LookupFrameResolutionMode reads (frame_resolution_mode, frame_timeout_ms) for the
// instance's template. Returns (mode, timeoutMs, nil) on success;
// ("", 0, sql.ErrNoRows) when the instance is missing.
func (s *framesImpl) LookupFrameResolutionMode(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (persistence.FrameResolutionMode, int64, error) {
	var (
		mode           string
		frameTimeoutMs int64
	)
	err := s.q(tx).QueryRow(ctx, `
        SELECT COALESCE(t.spec->>'frame_resolution_mode', '') AS mode,
               COALESCE(NULLIF((t.spec->>'frame_timeout_ms'),'')::bigint, 600000) AS frame_timeout_ms
        FROM rimsky_instances i
        JOIN rimsky_templates  t ON t.id = i.template_hash
        WHERE i.id = $1
    `, instanceID).Scan(&mode, &frameTimeoutMs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, fmt.Errorf("frames.LookupFrameResolutionMode: instance %s not found", instanceID)
		}
		return "", 0, fmt.Errorf("frames.LookupFrameResolutionMode: %w", err)
	}
	if frameTimeoutMs <= 0 {
		frameTimeoutMs = 600000
	}
	return persistence.FrameResolutionMode(mode), frameTimeoutMs, nil
}

// EnqueueSerialFrame inserts a queued serial_queue frame.
func (s *framesImpl) EnqueueSerialFrame(
	ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx persistence.Tx,
) (shared.UUID, error) {
	var frameID shared.UUID
	err := s.q(tx).QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, frame_resolution_mode, state, source_node_ids, queued_at, frame_timeout_ms)
        VALUES ($1, 'serial_queue', 'queued', ARRAY[$2]::UUID[], now(), $3)
        RETURNING frame_id
    `, instanceID, sourceNodeID, frameTimeoutMs).Scan(&frameID)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("frames.EnqueueSerialFrame: %w", err)
	}
	return frameID, nil
}

// EnqueueCoalesceFrame inserts a queued coalesce frame, or appends the
// source node to an existing pending coalesce row for the instance.
//
// Spec §7.3 step 1: keyed on the partial unique index
// uq_rimsky_frames_coalesce_queued (instance_id) WHERE state='queued'
// AND mode='coalesce'. Two concurrent producers (each in their own tx)
// must not deadlock or 5xx — exactly one wins the INSERT, all others
// fall through DO UPDATE and append source_node_ids.
func (s *framesImpl) EnqueueCoalesceFrame(
	ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx persistence.Tx,
) (shared.UUID, error) {
	var frameID shared.UUID
	err := s.q(tx).QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, frame_resolution_mode, state, source_node_ids, queued_at, frame_timeout_ms)
        VALUES ($1, 'coalesce', 'queued', ARRAY[$2]::UUID[], now(), $3)
        ON CONFLICT (instance_id) WHERE state = 'queued' AND frame_resolution_mode = 'coalesce'
        DO UPDATE SET source_node_ids = (
            CASE WHEN $2 = ANY(rimsky_frames.source_node_ids) THEN rimsky_frames.source_node_ids
                 ELSE array_append(rimsky_frames.source_node_ids, $2)
            END
        )
        RETURNING frame_id
    `, instanceID, sourceNodeID, frameTimeoutMs).Scan(&frameID)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("frames.EnqueueCoalesceFrame: %w", err)
	}
	return frameID, nil
}

// ListRunningFramesWithSources returns running frames along with their
// source_node_ids. Used by the runtime upstream-refresh pre-stage sweep.
func (s *framesImpl) ListRunningFramesWithSources(
	ctx context.Context, tx persistence.Tx,
) ([]persistence.FrameRunningSources, error) {
	rows, err := s.q(tx).Query(ctx, `
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
		var r persistence.FrameRunningSources
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &r.SourceNodeIDs); err != nil {
			return nil, fmt.Errorf("frames.ListRunningFramesWithSources: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// AppendSourceToQueuedFrame appends a node id to a queued frame's
// source_node_ids array (idempotent — no-op if the id is already
// present). The WHERE clause restricts the update to queued frames
// only, so a frame that has since been promoted to running or marked
// terminal silently no-ops. Used by invalidateNextFrame to attach
// upstream-refresh upstreams to the same frame as the receiver.
func (s *framesImpl) AppendSourceToQueuedFrame(
	ctx context.Context, frameID, nodeID shared.UUID, tx persistence.Tx,
) error {
	_, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_frames
        SET source_node_ids = (
            CASE WHEN $2 = ANY(source_node_ids) THEN source_node_ids
                 ELSE array_append(source_node_ids, $2)
            END
        )
        WHERE frame_id = $1 AND state = 'queued'
    `, frameID, nodeID)
	if err != nil {
		return fmt.Errorf("frames.AppendSourceToQueuedFrame: %w", err)
	}
	return nil
}

// ListForObservability returns frames matching filter for the
// /v1/observability/frames endpoint. Cursor pagination over (queued_at
// DESC, frame_id DESC). Cursor is a base64-JSON encoding of the last
// (queued_at, frame_id) pair on the previous page.
func (s *framesImpl) ListForObservability(ctx context.Context, filter persistence.FrameListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.FrameRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var instArg any
	if filter.InstanceID != nil {
		instArg = *filter.InstanceID
	}
	var stateArg any
	if filter.State != "" {
		stateArg = string(filter.State)
	}
	var cursorQueued *time.Time
	var cursorFrameID *shared.UUID
	if pag.Cursor != "" {
		q, fid, err := decodeFrameCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, fmt.Errorf("frames.list: bad cursor: %w", err)
		}
		cursorQueued = &q
		cursorFrameID = &fid
	}
	var qArg, fArg any
	if cursorQueued != nil {
		qArg = *cursorQueued
		fArg = *cursorFrameID
	}
	rows, err := s.q(tx).Query(ctx,
		`SELECT frame_id, instance_id, state, frame_resolution_mode, started_at, ended_at,
		        frame_timeout_ms, queued_at
		   FROM rimsky_frames
		  WHERE ($1::uuid IS NULL OR instance_id = $1)
		    AND ($2::text IS NULL OR state = $2)
		    AND ($3::timestamptz IS NULL OR (queued_at, frame_id) < ($3, $4))
		  ORDER BY queued_at DESC, frame_id DESC
		  LIMIT $5`,
		instArg, stateArg, qArg, fArg, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.FrameRow]{}, err
	}
	defer rows.Close()
	var out []persistence.FrameRow
	var lastQueued time.Time
	for rows.Next() {
		var (
			r     persistence.FrameRow
			state string
			mode  string
			qAt   time.Time
		)
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &state, &mode, &r.StartedAt, &r.EndedAt, &r.FrameTimeoutMs, &qAt); err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		r.State = persistence.FrameState(state)
		r.Mode = persistence.FrameResolutionMode(mode)
		lastQueued = qAt
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.FrameRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = encodeFrameCursor(lastQueued, out[len(out)-1].FrameID)
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

// RefreshProgress updates rimsky_frames.last_progress_at to NOW() for
// the given frame. Called by the node-state-transition write path on
// every UpdateState that carries the frame's id, so frame_timeout_ms
// measures no-progress-in-window rather than frame age.
//
// See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §7.
func (s *framesImpl) RefreshProgress(ctx context.Context, frameID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_frames SET last_progress_at = NOW() WHERE frame_id = $1`,
		frameID,
	)
	if err != nil {
		return fmt.Errorf("frames.RefreshProgress: %w", err)
	}
	return nil
}

// PruneTraceForRetention deletes terminal frame ROWS (cascading their
// node_runs via the rimsky_node_runs.frame_id ON DELETE CASCADE) older
// than `cutoff` OR beyond the `recentFramesKept` most-recent terminal
// frames per instance — the lesser-of bound. Returns the number of frame
// rows deleted.
//
// This replaces the prior node-run-only prune: we now delete the frame
// ROW itself and rely on the ON DELETE CASCADE from rimsky_frames to
// rimsky_node_runs to remove its runs, so a long-lived durable instance's
// frame backlog cannot grow without bound. In-flight frames
// (queued/running, including parked-held) are exempt — only
// state IN ('completed','failed') rows are candidates, so nothing live
// is ever reaped.
//
// `recentFramesKept <= 0` disables the count bound; a zero `cutoff`
// (time.Time{}) disables the time bound. Both disabled → no-op (returns
// 0). When both are active a frame is reaped if EITHER predicate matches
// (the lesser-of retention — whichever keeps fewer frames).
func (s *framesImpl) PruneTraceForRetention(ctx context.Context, recentFramesKept int, cutoff time.Time) (int, error) {
	countBound := recentFramesKept > 0
	timeBound := !cutoff.IsZero()
	if !countBound && !timeBound {
		return 0, nil
	}
	// @deliberate: sentinel binds let one SQL statement serve all three
	// bound combinations — a disabled bound's predicate is made
	// unsatisfiable (rk > a huge cap never matches; a NULL cutoff makes
	// the time predicate NULL→false) so the active bound(s) drive the
	// delete.
	var countCap int = recentFramesKept
	if !countBound {
		// @constraint: math.MaxInt (not a 1<<62 literal) keeps the
		// constant in range of int on a 32-bit build target, where 1<<62
		// would overflow and fail to compile.
		countCap = math.MaxInt
	}
	var cutoffArg any
	if timeBound {
		cutoffArg = cutoff
	}
	// @deliberate: standalone retention sweep runs directly against the
	// pool (no caller-supplied tx), mirroring Lineage.DeleteOlderThan.
	// The frame→node_run ON DELETE CASCADE removes each deleted frame's
	// runs.
	tag, err := (*tablesImpl)(s).pool.Exec(ctx, `
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
            WHERE ranked.rk > $1
               OR ($2::timestamptz IS NOT NULL AND ranked.ended_at < $2)
        )
    `, countCap, cutoffArg)
	if err != nil {
		return 0, fmt.Errorf("frames.PruneTraceForRetention: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// CountHeldFrames returns the number of running frames that have at
// least one parked rimsky_node_runs row attached via frame_id.
// Mirrors the predicate in /admin/diagnostics/held-frames.
//
// unresolved-work: counts parked (by definition — this query exists to
// count frames a parked run is holding open; consistent with
// ListRunningFramesNoPendingNodes treating parked as unresolved).
func (s *framesImpl) CountHeldFrames(ctx context.Context, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRow(ctx,
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
		r     persistence.FrameRow
		state string
		mode  string
	)
	err := s.q(tx).QueryRow(ctx,
		`SELECT frame_id, instance_id, state, frame_resolution_mode, started_at, ended_at, frame_timeout_ms
		   FROM rimsky_frames WHERE frame_id = $1`,
		frameID,
	).Scan(&r.FrameID, &r.InstanceID, &state, &mode, &r.StartedAt, &r.EndedAt, &r.FrameTimeoutMs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.State = persistence.FrameState(state)
	r.Mode = persistence.FrameResolutionMode(mode)
	return &r, nil
}
