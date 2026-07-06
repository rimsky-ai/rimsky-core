// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func (s *framesImpl) ListRunningFramesNoPendingNodes(ctx context.Context, tx persistence.Tx) ([]persistence.FramePending, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
        SELECT f.frame_id, f.instance_id
        FROM rimsky_frames f
        WHERE f.state = 'running'
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

func (s *framesImpl) PruneTraceForRetention(ctx context.Context, recentFramesKept int, cutoff time.Time) (int, error) {
	countBound := recentFramesKept > 0
	timeBound := !cutoff.IsZero()
	if !countBound && !timeBound {
		return 0, nil
	}
	countCap := recentFramesKept
	if !countBound {
		countCap = math.MaxInt
	}
	cutoffArg := formatTime(cutoff)
	if !timeBound {
		cutoffArg = formatTime(time.Time{})
	}
	res, err := (*tablesImpl)(s).db.ExecContext(ctx, `
        DELETE FROM rimsky_frames
         WHERE frame_id IN (
            SELECT frame_id FROM (
                SELECT f.frame_id, f.ended_at,
                       ROW_NUMBER() OVER (
                           PARTITION BY f.instance_id
                           ORDER BY COALESCE(f.ended_at, f.started_at) DESC, f.frame_id DESC
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

func (s *framesImpl) MarkSourceNodeStale(
	ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx persistence.Tx,
) (bool, error) {
	// @concept: run-scope
	res, err := s.q(tx).ExecContext(ctx, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
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
               ?, 'stale', 'cascade',
               COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = ? AND run_scope_id = f.root_run_scope_id), 0) + 1,
               ?, f.root_run_scope_id
          FROM rimsky_nodes n
          JOIN rimsky_frames f ON f.frame_id = ?
         WHERE n.id = ?
           AND n.instance_id = ?
           AND NOT EXISTS (
             SELECT 1 FROM rimsky_node_runs r
              WHERE r.node_id = ?
                AND r.state IN ('pending','stale','running','held','parked')
           )
    `, uuid.New().String(), nowUTC(), nodeID.String(), frameID.String(), frameID.String(), nodeID.String(), instanceID.String(), nodeID.String())
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
               AND r.state = 'stale'
               AND r.frame_id = ?
        )
    `, nodeID.String(), frameID.String()).Scan(&anyMatched); err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: existence check: %w", err)
	}
	return anyMatched != 0, nil
}

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
                AND r.state IN ('pending','stale','running','held')
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
		out = append(out, persistence.OrphanFrameDispatch{NodeRunID: did, ClaimedBy: claimedBy, FrameID: fid})
	}
	return out, rows.Err()
}

func (s *framesImpl) LookupFrameTimeoutMs(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (int64, error) {
	var frameTimeoutMs sql.NullInt64
	err := s.q(tx).QueryRowContext(ctx, `
        SELECT json_extract(t.spec, '$.frame_timeout_ms') AS frame_timeout_ms
        FROM rimsky_instances i
        JOIN rimsky_templates  t ON t.id = i.template_hash
        WHERE i.id = ?
    `, instanceID.String()).Scan(&frameTimeoutMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("frames.LookupFrameTimeoutMs: instance %s not found", instanceID)
		}
		return 0, fmt.Errorf("frames.LookupFrameTimeoutMs: %w", err)
	}
	timeout := int64(600000)
	if frameTimeoutMs.Valid && frameTimeoutMs.Int64 > 0 {
		timeout = frameTimeoutMs.Int64
	}
	return timeout, nil
}

func (s *framesImpl) InsertRunningFrame(
	ctx context.Context, instanceID, triggeringMessageID, rootRunScopeID shared.UUID, frameTimeoutMs int64, tx persistence.Tx,
) (shared.UUID, error) {
	frameID := uuid.New()
	now := nowUTC()
	_, err := s.q(tx).ExecContext(ctx, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, triggering_message_id, root_run_scope_id, state, started_at, frame_timeout_ms, last_progress_at)
        VALUES (?, ?, ?, ?, 'running', ?, ?, ?)
    `, frameID.String(), instanceID.String(), triggeringMessageID.String(), rootRunScopeID.String(),
		now, frameTimeoutMs, now)
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
	var instArg, stateArg, triggerArg any
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.State != "" {
		stateArg = string(filter.State)
	}
	if filter.TriggeringMessageID != nil {
		triggerArg = filter.TriggeringMessageID.String()
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
		`SELECT frame_id, instance_id, state, triggering_message_id, root_run_scope_id, started_at, ended_at, last_progress_at, frame_timeout_ms
		   FROM rimsky_frames
		  WHERE (? IS NULL OR instance_id = ?)
		    AND (? IS NULL OR state = ?)
		    AND (? IS NULL OR (started_at, frame_id) < (?, ?))
		    AND (? IS NULL OR triggering_message_id = ?)
		  ORDER BY started_at DESC, frame_id DESC
		  LIMIT ?`,
		instArg, instArg, stateArg, stateArg, cursorQAt, cursorQAt, cursorFid, triggerArg, triggerArg, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.FrameRow]{}, err
	}
	defer rows.Close()
	var out []persistence.FrameRow
	var lastQAt time.Time
	for rows.Next() {
		var (
			r              persistence.FrameRow
			frameID        string
			instanceID     string
			state          string
			triggeringMsg  string
			rootScope      string
			startedAt      sql.NullString
			endedAt        sql.NullString
			lastProgressAt sql.NullString
		)
		if err := rows.Scan(&frameID, &instanceID, &state, &triggeringMsg, &rootScope, &startedAt, &endedAt, &lastProgressAt, &r.FrameTimeoutMs); err != nil {
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
		mid, err := uuid.Parse(triggeringMsg)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		r.FrameID = fid
		r.InstanceID = iid
		r.State = persistence.FrameState(state)
		r.TriggeringMessageID = mid
		if rootScope != "" {
			rsid, err := uuid.Parse(rootScope)
			if err != nil {
				return persistence.PaginatedListResult[persistence.FrameRow]{}, fmt.Errorf("frames.ListForObservability: root_run_scope_id: %w", err)
			}
			r.RootRunScopeID = rsid
		}
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
		if lastProgressAt.Valid {
			t, err := parseTime(lastProgressAt.String)
			if err == nil {
				r.LastProgressAt = &t
			}
		}
		if startedAt.Valid {
			if t, err := parseTime(startedAt.String); err == nil {
				lastQAt = t
			}
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

func (s *framesImpl) CountHeldFrames(ctx context.Context, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT f.frame_id)
		   FROM rimsky_frames f
		   JOIN rimsky_node_runs d ON d.frame_id = f.frame_id
		  WHERE f.state = 'running' AND d.state = 'parked'`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("frames.CountHeldFrames: %w", err)
	}
	return n, nil
}

func (s *framesImpl) GetForObservability(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRow, error) {
	var (
		r              persistence.FrameRow
		fidStr         string
		iidStr         string
		state          string
		triggeringMsg  string
		rootScope      string
		startedAt      sql.NullString
		endedAt        sql.NullString
		lastProgressAt sql.NullString
	)
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT frame_id, instance_id, state, triggering_message_id, root_run_scope_id, started_at, ended_at, last_progress_at, frame_timeout_ms
		   FROM rimsky_frames WHERE frame_id = ?`,
		frameID.String(),
	).Scan(&fidStr, &iidStr, &state, &triggeringMsg, &rootScope, &startedAt, &endedAt, &lastProgressAt, &r.FrameTimeoutMs)
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
	mid, err := uuid.Parse(triggeringMsg)
	if err != nil {
		return nil, err
	}
	r.FrameID = fid
	r.InstanceID = iid
	r.State = persistence.FrameState(state)
	r.TriggeringMessageID = mid
	if rootScope != "" {
		rsid, err := uuid.Parse(rootScope)
		if err != nil {
			return nil, fmt.Errorf("frames.GetForObservability: root_run_scope_id: %w", err)
		}
		r.RootRunScopeID = rsid
	}
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
	if lastProgressAt.Valid {
		t, err := parseTime(lastProgressAt.String)
		if err == nil {
			r.LastProgressAt = &t
		}
	}
	return &r, nil
}

func (s *framesImpl) GetForObservabilityWithMessage(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRowWithMessage, error) {
	var (
		r              persistence.FrameRowWithMessage
		fidStr         string
		iidStr         string
		state          string
		triggeringMsg  string
		rootScope      string
		startedAt      sql.NullString
		endedAt        sql.NullString
		lastProgressAt sql.NullString
		mType          sql.NullString
		mSender        sql.NullString
		mKind          sql.NullString
	)
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT f.frame_id, f.instance_id, f.state, f.triggering_message_id, f.root_run_scope_id,
		        f.started_at, f.ended_at, f.last_progress_at, f.frame_timeout_ms,
		        m.type, m.sender, m.sender_kind
		   FROM rimsky_frames f
		   LEFT JOIN rimsky_messages m ON m.id = f.triggering_message_id
		  WHERE f.frame_id = ?`,
		frameID.String(),
	).Scan(&fidStr, &iidStr, &state, &triggeringMsg, &rootScope, &startedAt, &endedAt, &lastProgressAt, &r.FrameTimeoutMs,
		&mType, &mSender, &mKind)
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
	mid, err := uuid.Parse(triggeringMsg)
	if err != nil {
		return nil, err
	}
	r.FrameID = fid
	r.InstanceID = iid
	r.State = persistence.FrameState(state)
	r.TriggeringMessageID = mid
	if rootScope != "" {
		rsid, err := uuid.Parse(rootScope)
		if err != nil {
			return nil, fmt.Errorf("frames.GetForObservabilityWithMessage: root_run_scope_id: %w", err)
		}
		r.RootRunScopeID = rsid
	}
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
	if lastProgressAt.Valid {
		t, err := parseTime(lastProgressAt.String)
		if err == nil {
			r.LastProgressAt = &t
		}
	}
	if mType.Valid {
		r.MessageType = mType.String
	}
	if mSender.Valid {
		r.MessageSender = mSender.String
	}
	if mKind.Valid {
		r.MessageSenderKind = mKind.String
	}
	return &r, nil
}

func (s *framesImpl) ListForObservabilityWithMessage(ctx context.Context, filter persistence.FrameListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.FrameRowWithMessage], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var instArg, stateArg, triggerArg any
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.State != "" {
		stateArg = string(filter.State)
	}
	if filter.TriggeringMessageID != nil {
		triggerArg = filter.TriggeringMessageID.String()
	}
	var cursorQAt, cursorFid any
	if pag.Cursor != "" {
		q, fid, err := decodeFrameCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, fmt.Errorf("frames.list: bad cursor: %w", err)
		}
		cursorQAt = formatTime(q)
		cursorFid = fid.String()
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT f.frame_id, f.instance_id, f.state, f.triggering_message_id, f.root_run_scope_id,
		        f.started_at, f.ended_at, f.last_progress_at, f.frame_timeout_ms,
		        m.type, m.sender, m.sender_kind
		   FROM rimsky_frames f
		   LEFT JOIN rimsky_messages m ON m.id = f.triggering_message_id
		  WHERE (? IS NULL OR f.instance_id = ?)
		    AND (? IS NULL OR f.state = ?)
		    AND (? IS NULL OR (f.started_at, f.frame_id) < (?, ?))
		    AND (? IS NULL OR f.triggering_message_id = ?)
		  ORDER BY f.started_at DESC, f.frame_id DESC
		  LIMIT ?`,
		instArg, instArg, stateArg, stateArg, cursorQAt, cursorQAt, cursorFid, triggerArg, triggerArg, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
	}
	defer rows.Close()
	var out []persistence.FrameRowWithMessage
	var lastQAt time.Time
	for rows.Next() {
		var (
			r              persistence.FrameRowWithMessage
			frameID        string
			instanceID     string
			state          string
			triggeringMsg  string
			rootScope      string
			startedAt      sql.NullString
			endedAt        sql.NullString
			lastProgressAt sql.NullString
			mType          sql.NullString
			mSender        sql.NullString
			mKind          sql.NullString
		)
		if err := rows.Scan(&frameID, &instanceID, &state, &triggeringMsg, &rootScope, &startedAt, &endedAt, &lastProgressAt, &r.FrameTimeoutMs,
			&mType, &mSender, &mKind); err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
		}
		fid, err := uuid.Parse(frameID)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
		}
		iid, err := uuid.Parse(instanceID)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
		}
		mid, err := uuid.Parse(triggeringMsg)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
		}
		r.FrameID = fid
		r.InstanceID = iid
		r.State = persistence.FrameState(state)
		r.TriggeringMessageID = mid
		if rootScope != "" {
			rsid, err := uuid.Parse(rootScope)
			if err != nil {
				return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, fmt.Errorf("frames.ListForObservabilityWithMessage: root_run_scope_id: %w", err)
			}
			r.RootRunScopeID = rsid
		}
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
		if lastProgressAt.Valid {
			t, err := parseTime(lastProgressAt.String)
			if err == nil {
				r.LastProgressAt = &t
			}
		}
		if mType.Valid {
			r.MessageType = mType.String
		}
		if mSender.Valid {
			r.MessageSender = mSender.String
		}
		if mKind.Valid {
			r.MessageSenderKind = mKind.String
		}
		if startedAt.Valid {
			if t, err := parseTime(startedAt.String); err == nil {
				lastQAt = t
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = encodeFrameCursor(lastQAt, out[len(out)-1].FrameID)
	}
	return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{Rows: out, NextCursor: nextCursor}, nil
}
