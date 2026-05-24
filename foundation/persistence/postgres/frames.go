// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// frames.go is the postgres accessor for `rimsky_frames` and the related
// frame-engine SQL on `rimsky_nodes`, `rimsky_node_runs`, and
// `rimsky_instances`. Owns the SQL the frame engine
// (graph/frame/{engine,producer}.go) calls through `persistence.FrameTable`.

package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// ListRunningFramesNoPendingNodes returns running frames whose nodes in
// the same (instance_id, frame_id) scope have no in-flight run row in
// state IN ('stale','running').
//
// Post-stage-3: state lives on rimsky_node_runs only. `parked` and
// `failed` terminal rows do not count toward the frame's pending set
// (parked is held but not actively contributing; failed is terminal).
func (s *framesImpl) ListRunningFramesNoPendingNodes(ctx context.Context, tx persistence.Tx) ([]persistence.FramePending, error) {
	rows, err := s.q(tx).Query(ctx, `
        SELECT f.frame_id, f.instance_id
        FROM rimsky_frames f
        WHERE f.state = 'running'
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_node_runs r
              WHERE r.frame_id = f.frame_id
                AND r.phase IN ('pending','active','held')
                AND r.state IN ('stale','running')
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

// MarkInstanceTerminatedIfDone sets rimsky_instances.terminated_at=now()
// when the terminal predicate holds. Idempotent set-once.
//
// Post-stage-3: the "still working" predicate is "an in-flight run row
// in state IN ('stale','running') exists for any node in this
// instance". Mirrors ListRunningFramesNoPendingNodes' predicate so
// frame-end and instance-terminated agree.
func (s *framesImpl) MarkInstanceTerminatedIfDone(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_instances i
        SET terminated_at = now()
        WHERE i.id = $1
          AND i.terminated_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_frames f
              WHERE f.instance_id = i.id AND f.state IN ('queued','running')
          )
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_node_runs r
              JOIN rimsky_nodes n ON n.id = r.node_id
              WHERE n.instance_id = i.id
                AND r.phase IN ('pending','active','held')
                AND r.state IN ('stale','running')
          )
    `, instanceID)
	if err != nil {
		return fmt.Errorf("frames.MarkInstanceTerminatedIfDone: %w", err)
	}
	return nil
}

// ListQueuedFramesReadyToStart returns at most one queued frame per
// instance — the oldest queued — for instances that have no
// currently-running frame.
func (s *framesImpl) ListQueuedFramesReadyToStart(ctx context.Context, tx persistence.Tx) ([]persistence.FrameQueuedReady, error) {
	rows, err := s.q(tx).Query(ctx, `
        SELECT DISTINCT ON (f.instance_id)
            f.frame_id, f.instance_id, f.source_node_ids
        FROM rimsky_frames f
        WHERE f.state = 'queued'
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
	// Bind the node's frame_id (idempotent for re-entry).
	if _, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_nodes
        SET frame_id = $1, updated_at = now()
        WHERE instance_id = $2 AND id = $3
    `, frameID, instanceID, nodeID); err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: bind frame: %w", err)
	}
	// INSERT a fresh pending run row when no in-flight row exists.
	// Populates required_stores from the template node-def so the
	// supervisor's SelectCandidates pool-predicate
	// (required_stores ⊆ accepted_stores) routes the row correctly.
	//
	// Under RunScope-first the new row lives in the instance's main
	// RunScope (the only RunScope a frame source's run can belong to;
	// sub-graph + fan-out children allocate via AffirmNodeRunRow /
	// CreateFanOutChildren, not via the frame source path).
	// @concept: run-scope
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
	// No INSERT — fall back to "already in-flight stale row pinned to
	// this frame" (under-contention re-entry).
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

// PruneOldRunsForRetention deletes rimsky_node_runs rows whose owning
// frame is older than the `recentFramesKept`-th most-recent terminal
// frame for the same instance. Returns the number of rows deleted.
//
// In-flight frames (queued/running) are exempt from retention — the
// "keep N most-recent" cap counts only terminal frames so an
// long-running instance can accumulate retention beyond N if many of
// its frames are concurrently in flight (a degenerate case that the
// retention warning surface — outside this method — picks up).
func (s *framesImpl) PruneOldRunsForRetention(ctx context.Context, recentFramesKept int) (int, error) {
	if recentFramesKept <= 0 {
		return 0, nil
	}
	// PARTITION BY instance_id ORDER BY ended_at DESC gives each frame a
	// per-instance rank (1 = most recent terminal). Anything with rank >
	// recentFramesKept is a prune candidate. DELETE from rimsky_node_runs
	// where frame_id matches the candidates. The ON DELETE CASCADE from
	// rimsky_frames to rimsky_node_runs would also fire if we deleted
	// the frames themselves — but the retention sweep keeps the frame
	// row (so observability can still surface it) and deletes only the
	// associated run rows.
	tag, err := s.q(nil).Exec(ctx, `
        DELETE FROM rimsky_node_runs
        WHERE frame_id IN (
            SELECT frame_id FROM (
                SELECT f.frame_id,
                       ROW_NUMBER() OVER (
                           PARTITION BY f.instance_id
                           ORDER BY COALESCE(f.ended_at, f.queued_at) DESC, f.frame_id DESC
                       ) AS rk
                  FROM rimsky_frames f
                 WHERE f.state IN ('completed','failed')
            ) ranked
            WHERE ranked.rk > $1
        )
    `, recentFramesKept)
	if err != nil {
		return 0, fmt.Errorf("frames.PruneOldRunsForRetention: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// CountHeldFrames returns the number of running frames that have at
// least one parked rimsky_node_runs row attached via frame_id.
// Mirrors the predicate in /admin/diagnostics/held-frames.
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
