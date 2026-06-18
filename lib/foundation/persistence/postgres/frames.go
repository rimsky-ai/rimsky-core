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

func (s *framesImpl) ListQueuedFramesReadyToStart(ctx context.Context, tx persistence.Tx) ([]persistence.FrameQueuedReady, error) {
	rows, err := s.q(tx).Query(ctx, `
        SELECT DISTINCT ON (f.instance_id)
            f.frame_id, f.instance_id, f.triggering_message_id
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
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &r.TriggeringMessageID); err != nil {
			return nil, fmt.Errorf("frames.ListQueuedFramesReadyToStart: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

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

func (s *framesImpl) MarkSourceNodeStale(
	ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx persistence.Tx,
) (bool, error) {
	if _, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_nodes
        SET frame_id = $1, updated_at = now()
        WHERE instance_id = $2 AND id = $3
    `, frameID, instanceID, nodeID); err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: bind frame: %w", err)
	}
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

func (s *framesImpl) LookupFrameTimeoutMs(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (int64, error) {
	var frameTimeoutMs int64
	err := s.q(tx).QueryRow(ctx, `
        SELECT COALESCE(NULLIF((t.spec->>'frame_timeout_ms'),'')::bigint, 600000) AS frame_timeout_ms
        FROM rimsky_instances i
        JOIN rimsky_templates  t ON t.id = i.template_hash
        WHERE i.id = $1
    `, instanceID).Scan(&frameTimeoutMs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("frames.LookupFrameTimeoutMs: instance %s not found", instanceID)
		}
		return 0, fmt.Errorf("frames.LookupFrameTimeoutMs: %w", err)
	}
	if frameTimeoutMs <= 0 {
		frameTimeoutMs = 600000
	}
	return frameTimeoutMs, nil
}

func (s *framesImpl) InsertFrame(
	ctx context.Context, instanceID, triggeringMessageID shared.UUID, frameTimeoutMs int64, tx persistence.Tx,
) (shared.UUID, error) {
	var frameID shared.UUID
	err := s.q(tx).QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, triggering_message_id, state, queued_at, frame_timeout_ms)
        VALUES ($1, $2, 'queued', now(), $3)
        RETURNING frame_id
    `, instanceID, triggeringMessageID, frameTimeoutMs).Scan(&frameID)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("frames.InsertFrame: %w", err)
	}
	return frameID, nil
}

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
	var triggerArg any
	if filter.TriggeringMessageID != nil {
		triggerArg = *filter.TriggeringMessageID
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
		`SELECT frame_id, instance_id, state, triggering_message_id, started_at, ended_at,
		        last_progress_at, frame_timeout_ms, queued_at
		   FROM rimsky_frames
		  WHERE ($1::uuid IS NULL OR instance_id = $1)
		    AND ($2::text IS NULL OR state = $2)
		    AND ($3::timestamptz IS NULL OR (queued_at, frame_id) < ($3, $4))
		    AND ($6::uuid IS NULL OR triggering_message_id = $6)
		  ORDER BY queued_at DESC, frame_id DESC
		  LIMIT $5`,
		instArg, stateArg, qArg, fArg, limit, triggerArg,
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
			qAt   time.Time
		)
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &state, &r.TriggeringMessageID,
			&r.StartedAt, &r.EndedAt, &r.LastProgressAt, &r.FrameTimeoutMs, &qAt); err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		r.State = persistence.FrameState(state)
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

func (s *framesImpl) PruneTraceForRetention(ctx context.Context, recentFramesKept int, cutoff time.Time) (int, error) {
	countBound := recentFramesKept > 0
	timeBound := !cutoff.IsZero()
	if !countBound && !timeBound {
		return 0, nil
	}
	var countCap int = recentFramesKept
	if !countBound {
		countCap = math.MaxInt
	}
	var cutoffArg any
	if timeBound {
		cutoffArg = cutoff
	}
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

func (s *framesImpl) GetForObservability(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRow, error) {
	var (
		r     persistence.FrameRow
		state string
	)
	err := s.q(tx).QueryRow(ctx,
		`SELECT frame_id, instance_id, state, triggering_message_id, started_at, ended_at, last_progress_at, frame_timeout_ms
		   FROM rimsky_frames WHERE frame_id = $1`,
		frameID,
	).Scan(&r.FrameID, &r.InstanceID, &state, &r.TriggeringMessageID, &r.StartedAt, &r.EndedAt, &r.LastProgressAt, &r.FrameTimeoutMs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.State = persistence.FrameState(state)
	return &r, nil
}

func (s *framesImpl) GetForObservabilityWithMessage(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRowWithMessage, error) {
	var (
		r       persistence.FrameRowWithMessage
		state   string
		mType   *string
		mSender *string
		mKind   *string
	)
	err := s.q(tx).QueryRow(ctx,
		`SELECT f.frame_id, f.instance_id, f.state, f.triggering_message_id,
		        f.started_at, f.ended_at, f.last_progress_at, f.frame_timeout_ms,
		        m.type, m.sender, m.sender_kind
		   FROM rimsky_frames f
		   LEFT JOIN rimsky_messages m ON m.id = f.triggering_message_id
		  WHERE f.frame_id = $1`,
		frameID,
	).Scan(&r.FrameID, &r.InstanceID, &state, &r.TriggeringMessageID,
		&r.StartedAt, &r.EndedAt, &r.LastProgressAt, &r.FrameTimeoutMs,
		&mType, &mSender, &mKind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.State = persistence.FrameState(state)
	if mType != nil {
		r.MessageType = *mType
	}
	if mSender != nil {
		r.MessageSender = *mSender
	}
	if mKind != nil {
		r.MessageSenderKind = *mKind
	}
	return &r, nil
}

func (s *framesImpl) ListForObservabilityWithMessage(ctx context.Context, filter persistence.FrameListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.FrameRowWithMessage], error) {
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
	var triggerArg any
	if filter.TriggeringMessageID != nil {
		triggerArg = *filter.TriggeringMessageID
	}
	var cursorQueued *time.Time
	var cursorFrameID *shared.UUID
	if pag.Cursor != "" {
		q, fid, err := decodeFrameCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, fmt.Errorf("frames.list: bad cursor: %w", err)
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
		`SELECT f.frame_id, f.instance_id, f.state, f.triggering_message_id,
		        f.started_at, f.ended_at, f.last_progress_at, f.frame_timeout_ms, f.queued_at,
		        m.type, m.sender, m.sender_kind
		   FROM rimsky_frames f
		   LEFT JOIN rimsky_messages m ON m.id = f.triggering_message_id
		  WHERE ($1::uuid IS NULL OR f.instance_id = $1)
		    AND ($2::text IS NULL OR f.state = $2)
		    AND ($3::timestamptz IS NULL OR (f.queued_at, f.frame_id) < ($3, $4))
		    AND ($6::uuid IS NULL OR f.triggering_message_id = $6)
		  ORDER BY f.queued_at DESC, f.frame_id DESC
		  LIMIT $5`,
		instArg, stateArg, qArg, fArg, limit, triggerArg,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
	}
	defer rows.Close()
	var out []persistence.FrameRowWithMessage
	var lastQueued time.Time
	for rows.Next() {
		var (
			r       persistence.FrameRowWithMessage
			state   string
			qAt     time.Time
			mType   *string
			mSender *string
			mKind   *string
		)
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &state, &r.TriggeringMessageID,
			&r.StartedAt, &r.EndedAt, &r.LastProgressAt, &r.FrameTimeoutMs, &qAt,
			&mType, &mSender, &mKind); err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
		}
		r.State = persistence.FrameState(state)
		if mType != nil {
			r.MessageType = *mType
		}
		if mSender != nil {
			r.MessageSender = *mSender
		}
		if mKind != nil {
			r.MessageSenderKind = *mKind
		}
		lastQueued = qAt
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = encodeFrameCursor(lastQueued, out[len(out)-1].FrameID)
	}
	return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{Rows: out, NextCursor: nextCursor}, nil
}
