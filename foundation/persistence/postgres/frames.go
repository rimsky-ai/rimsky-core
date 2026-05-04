// frames.go is the postgres accessor for `rimsky_frames` and the related
// frame-engine SQL on `rimsky_nodes`, `rimsky_dispatch`, and
// `rimsky_instances`. Owns the SQL the frame engine
// (core/frame/{engine,producer}.go) calls through `persistence.FrameStore`.

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
	"github.com/fallguy/rimsky/modeling/shared"
)

// ListRunningFramesNoPendingNodes returns running frames whose nodes in
// the same (instance_id, frame_id) scope are all out of stale/running.
//
// The NOT EXISTS predicate filters by `n.frame_id = f.frame_id` so a
// stale/running node from a different (concurrent or later) frame cannot
// block this frame's end. Under v1 there is at most one frame per
// instance in 'running' state (uq_rimsky_frames_running) and any
// in-flight node carries that frame's frame_id; the per-frame filter
// is robust under future Rule 3b parallel-buffered semantics
// (spec §10.6).
func (s *framesImpl) ListRunningFramesNoPendingNodes(ctx context.Context, tx persistence.Tx) ([]persistence.FramePending, error) {
	rows, err := s.q(tx).Query(ctx, `
        SELECT f.frame_id, f.instance_id
        FROM rimsky_frames f
        WHERE f.state = 'running'
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_nodes n
              WHERE n.instance_id = f.instance_id
                AND n.frame_id = f.frame_id
                AND n.state IN ('stale','running')
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

// HasFailedNode returns true when any rimsky_nodes row for the given
// (instanceID, frameID) is in state='failed'.
func (s *framesImpl) HasFailedNode(ctx context.Context, instanceID, frameID shared.UUID, tx persistence.Tx) (bool, error) {
	var anyFailed bool
	err := s.q(tx).QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM rimsky_nodes n
            WHERE n.instance_id = $1
              AND n.frame_id = $2
              AND n.state = 'failed'
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
              SELECT 1 FROM rimsky_nodes n
              WHERE n.instance_id = i.id AND n.state IN ('stale','running')
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

// MarkSourceNodeStale flips a frame's source node to stale-with-frame_id.
// Accepts the in-bounds states: fresh, failed, or stale-with-NULL-frame_id.
// Returns matched=true when exactly one row moved.
func (s *framesImpl) MarkSourceNodeStale(
	ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx persistence.Tx,
) (bool, error) {
	cmd, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_nodes
        SET state = 'stale', frame_id = $1, updated_at = now()
        WHERE instance_id = $2 AND id = $3
          AND (state IN ('fresh','failed')
               OR (state = 'stale' AND frame_id IS NULL))
    `, frameID, instanceID, nodeID)
	if err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

// ListStuckRunningFrames returns running frames past their timeout
// with no claimed dispatches and at least one stale/running node.
func (s *framesImpl) ListStuckRunningFrames(ctx context.Context, tx persistence.Tx) ([]persistence.FrameStuck, error) {
	rows, err := s.q(tx).Query(ctx, `
        SELECT f.frame_id, f.instance_id, f.frame_timeout_ms
        FROM rimsky_frames f
        WHERE f.state = 'running'
          AND f.started_at + (f.frame_timeout_ms || ' milliseconds')::interval < now()
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_dispatch d
              WHERE d.frame_id = f.frame_id AND d.claimed_by IS NOT NULL
          )
          AND EXISTS (
              SELECT 1 FROM rimsky_nodes n
              WHERE n.instance_id = f.instance_id AND n.state IN ('stale','running')
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

// FailAllPendingNodes flips every stale/running node for the instance
// to state='failed'.
func (s *framesImpl) FailAllPendingNodes(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_nodes
        SET state = 'failed', updated_at = now()
        WHERE instance_id = $1 AND state IN ('stale','running')
    `, instanceID)
	if err != nil {
		return fmt.Errorf("frames.FailAllPendingNodes: %w", err)
	}
	return nil
}

// ListOrphanFrameDispatches returns dispatch rows whose claim is non-NULL
// but whose owning frame has reached terminal state.
func (s *framesImpl) ListOrphanFrameDispatches(ctx context.Context, tx persistence.Tx) ([]persistence.OrphanFrameDispatch, error) {
	rows, err := s.q(tx).Query(ctx, `
        SELECT d.id, d.claimed_by, d.frame_id
        FROM rimsky_dispatch d
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

// LookupFrameMode reads (frame_resolution, frame_timeout_ms) for the
// instance's template. Returns (mode, timeoutMs, nil) on success;
// ("", 0, sql.ErrNoRows) when the instance is missing.
func (s *framesImpl) LookupFrameMode(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (persistence.FrameMode, int64, error) {
	var (
		mode           string
		frameTimeoutMs int64
	)
	err := s.q(tx).QueryRow(ctx, `
        SELECT COALESCE(t.spec->>'frame_resolution', '') AS mode,
               COALESCE(NULLIF((t.spec->>'frame_timeout_ms'),'')::bigint, 600000) AS frame_timeout_ms
        FROM rimsky_instances i
        JOIN rimsky_templates  t ON t.id = i.template_hash
        WHERE i.id = $1
    `, instanceID).Scan(&mode, &frameTimeoutMs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, fmt.Errorf("frames.LookupFrameMode: instance %s not found", instanceID)
		}
		return "", 0, fmt.Errorf("frames.LookupFrameMode: %w", err)
	}
	if frameTimeoutMs <= 0 {
		frameTimeoutMs = 600000
	}
	return persistence.FrameMode(mode), frameTimeoutMs, nil
}

// EnqueueSerialFrame inserts a queued serial_queue frame.
func (s *framesImpl) EnqueueSerialFrame(
	ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx persistence.Tx,
) (shared.UUID, error) {
	var frameID shared.UUID
	err := s.q(tx).QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms)
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
            (instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms)
        VALUES ($1, 'coalesce', 'queued', ARRAY[$2]::UUID[], now(), $3)
        ON CONFLICT (instance_id) WHERE state = 'queued' AND mode = 'coalesce'
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
		`SELECT frame_id, instance_id, state, mode, started_at, ended_at,
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
		r.Mode = persistence.FrameMode(mode)
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

// GetForObservability returns one frame by id.
func (s *framesImpl) GetForObservability(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRow, error) {
	var (
		r     persistence.FrameRow
		state string
		mode  string
	)
	err := s.q(tx).QueryRow(ctx,
		`SELECT frame_id, instance_id, state, mode, started_at, ended_at, frame_timeout_ms
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
	r.Mode = persistence.FrameMode(mode)
	return &r, nil
}
