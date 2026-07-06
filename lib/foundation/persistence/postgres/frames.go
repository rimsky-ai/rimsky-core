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
        WHERE f.ended_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_node_runs r
              WHERE r.frame_id = f.frame_id
                AND r.state IN ('pending','stale','running','held','parked')
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

func (s *framesImpl) MarkFrameEnded(
	ctx context.Context, frameID shared.UUID, tx persistence.Tx,
) (bool, error) {
	cmd, err := s.q(tx).Exec(ctx, `
        UPDATE rimsky_frames
        SET ended_at = now()
        WHERE frame_id = $1 AND ended_at IS NULL
    `, frameID)
	if err != nil {
		return false, fmt.Errorf("frames.MarkFrameEnded: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

func (s *framesImpl) GetRunningFrameID(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (*shared.UUID, error) {
	var frameID shared.UUID
	err := s.q(tx).QueryRow(ctx, `
        SELECT frame_id
          FROM rimsky_frames
         WHERE instance_id = $1 AND ended_at IS NULL
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
	// @concept: run-scope
	tag, err := s.q(tx).Exec(ctx, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
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
               NOW(), 'stale', 'cascade',
               COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = $2 AND run_scope_id = f.root_run_scope_id), 0) + 1,
               $1, f.root_run_scope_id
          FROM rimsky_nodes n
          JOIN rimsky_frames f ON f.frame_id = $1
         WHERE n.id = $2
           AND n.instance_id = $3
           AND NOT EXISTS (
             SELECT 1 FROM rimsky_node_runs r
              WHERE r.node_id = $2
                AND r.state IN ('pending','stale','running','held','parked')
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
        WHERE f.ended_at IS NULL
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
                AND r.state IN ('pending','stale','running','held')
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
          AND f.ended_at IS NOT NULL
    `)
	if err != nil {
		return nil, fmt.Errorf("frames.ListOrphanFrameDispatches: %w", err)
	}
	defer rows.Close()
	var out []persistence.OrphanFrameDispatch
	for rows.Next() {
		var r persistence.OrphanFrameDispatch
		if err := rows.Scan(&r.NodeRunID, &r.ClaimedBy, &r.FrameID); err != nil {
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

func (s *framesImpl) InsertRunningFrame(
	ctx context.Context, instanceID, triggeringMessageID, rootRunScopeID shared.UUID, frameTimeoutMs int64, tx persistence.Tx,
) (shared.UUID, error) {
	var frameID shared.UUID
	err := s.q(tx).QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, triggering_message_id, root_run_scope_id, started_at, frame_timeout_ms)
        VALUES ($1, $2, $3, now(), $4)
        RETURNING frame_id
    `, instanceID, triggeringMessageID, rootRunScopeID, frameTimeoutMs).Scan(&frameID)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("frames.InsertRunningFrame: %w", err)
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
	var unresolvedArg any
	if filter.Unresolved != nil {
		unresolvedArg = *filter.Unresolved
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
		`SELECT f.frame_id, f.instance_id,
		        CASE
		            WHEN f.ended_at IS NULL THEN 'running'
		            WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed'
		            ELSE 'completed'
		        END AS state,
		        f.triggering_message_id, f.root_run_scope_id, f.started_at, f.ended_at,
		        f.last_progress_at, f.frame_timeout_ms
		   FROM rimsky_frames f
		  WHERE ($1::uuid IS NULL OR f.instance_id = $1)
		    AND ($2::boolean IS NULL OR ($2 = TRUE AND f.ended_at IS NULL) OR ($2 = FALSE AND f.ended_at IS NOT NULL))
		    AND ($3::timestamptz IS NULL OR (f.started_at, f.frame_id) < ($3, $4))
		    AND ($6::uuid IS NULL OR f.triggering_message_id = $6)
		  ORDER BY f.started_at DESC, f.frame_id DESC
		  LIMIT $5`,
		instArg, unresolvedArg, qArg, fArg, limit, triggerArg,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.FrameRow]{}, err
	}
	defer rows.Close()
	var out []persistence.FrameRow
	var lastQueued time.Time
	for rows.Next() {
		var r persistence.FrameRow
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &r.State, &r.TriggeringMessageID, &r.RootRunScopeID,
			&r.StartedAt, &r.EndedAt, &r.LastProgressAt, &r.FrameTimeoutMs); err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		if r.StartedAt != nil {
			lastQueued = *r.StartedAt
		}
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
                           ORDER BY COALESCE(f.ended_at, f.started_at) DESC, f.frame_id DESC
                       ) AS rk
                  FROM rimsky_frames f
                 WHERE f.ended_at IS NOT NULL
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
		  WHERE f.ended_at IS NULL AND d.state = 'parked'`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("frames.CountHeldFrames: %w", err)
	}
	return n, nil
}

func (s *framesImpl) GetForObservability(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRow, error) {
	var r persistence.FrameRow
	err := s.q(tx).QueryRow(ctx,
		`SELECT f.frame_id, f.instance_id,
		        CASE
		            WHEN f.ended_at IS NULL THEN 'running'
		            WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed'
		            ELSE 'completed'
		        END AS state,
		        f.triggering_message_id, f.root_run_scope_id, f.started_at, f.ended_at, f.last_progress_at, f.frame_timeout_ms
		   FROM rimsky_frames f WHERE f.frame_id = $1`,
		frameID,
	).Scan(&r.FrameID, &r.InstanceID, &r.State, &r.TriggeringMessageID, &r.RootRunScopeID, &r.StartedAt, &r.EndedAt, &r.LastProgressAt, &r.FrameTimeoutMs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *framesImpl) GetForObservabilityWithMessage(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRowWithMessage, error) {
	var (
		r       persistence.FrameRowWithMessage
		mType   *string
		mSender *string
		mKind   *string
	)
	err := s.q(tx).QueryRow(ctx,
		`SELECT f.frame_id, f.instance_id,
		        CASE
		            WHEN f.ended_at IS NULL THEN 'running'
		            WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed'
		            ELSE 'completed'
		        END AS state,
		        f.triggering_message_id, f.root_run_scope_id,
		        f.started_at, f.ended_at, f.last_progress_at, f.frame_timeout_ms,
		        m.type, m.sender, m.sender_kind
		   FROM rimsky_frames f
		   LEFT JOIN rimsky_messages m ON m.id = f.triggering_message_id
		  WHERE f.frame_id = $1`,
		frameID,
	).Scan(&r.FrameID, &r.InstanceID, &r.State, &r.TriggeringMessageID, &r.RootRunScopeID,
		&r.StartedAt, &r.EndedAt, &r.LastProgressAt, &r.FrameTimeoutMs,
		&mType, &mSender, &mKind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
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
	var unresolvedArg any
	if filter.Unresolved != nil {
		unresolvedArg = *filter.Unresolved
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
		`SELECT f.frame_id, f.instance_id,
		        CASE
		            WHEN f.ended_at IS NULL THEN 'running'
		            WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed'
		            ELSE 'completed'
		        END AS state,
		        f.triggering_message_id, f.root_run_scope_id,
		        f.started_at, f.ended_at, f.last_progress_at, f.frame_timeout_ms,
		        m.type, m.sender, m.sender_kind
		   FROM rimsky_frames f
		   LEFT JOIN rimsky_messages m ON m.id = f.triggering_message_id
		  WHERE ($1::uuid IS NULL OR f.instance_id = $1)
		    AND ($2::boolean IS NULL OR ($2 = TRUE AND f.ended_at IS NULL) OR ($2 = FALSE AND f.ended_at IS NOT NULL))
		    AND ($3::timestamptz IS NULL OR (f.started_at, f.frame_id) < ($3, $4))
		    AND ($6::uuid IS NULL OR f.triggering_message_id = $6)
		  ORDER BY f.started_at DESC, f.frame_id DESC
		  LIMIT $5`,
		instArg, unresolvedArg, qArg, fArg, limit, triggerArg,
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
			mType   *string
			mSender *string
			mKind   *string
		)
		if err := rows.Scan(&r.FrameID, &r.InstanceID, &r.State, &r.TriggeringMessageID, &r.RootRunScopeID,
			&r.StartedAt, &r.EndedAt, &r.LastProgressAt, &r.FrameTimeoutMs,
			&mType, &mSender, &mKind); err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
		}
		if mType != nil {
			r.MessageType = *mType
		}
		if mSender != nil {
			r.MessageSender = *mSender
		}
		if mKind != nil {
			r.MessageSenderKind = *mKind
		}
		if r.StartedAt != nil {
			lastQueued = *r.StartedAt
		}
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
