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
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: frame
const frameStateCaseSQL = `CASE
    WHEN f.ended_at IS NULL THEN 'running'
    WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed'
                 AND (r.settling_signal_type IS NULL OR r.settling_signal_type <> '` + cascade.SettlingSignalInstanceKilled + `')) THEN 'failed'
    WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed'
                 AND r.settling_signal_type = '` + cascade.SettlingSignalInstanceKilled + `') THEN 'terminated'
    ELSE 'completed'
END`

func (s *framesImpl) ListRunningFramesNoPendingNodes(ctx context.Context, tx persistence.Tx) ([]persistence.FramePending, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
        SELECT f.frame_id, f.instance_id
        FROM rimsky_frames f
        WHERE f.ended_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_node_runs r
              WHERE r.frame_id = f.frame_id
                AND r.state IN (`+inFlightNodeRunStates+`)
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

func (s *framesImpl) MarkFrameEnded(
	ctx context.Context, frameID shared.UUID, tx persistence.Tx,
) (bool, error) {
	res, err := s.q(tx).ExecContext(ctx, `
        UPDATE rimsky_frames
        SET ended_at = ?
        WHERE frame_id = ? AND ended_at IS NULL
    `, nowUTC(), frameID.String())
	if err != nil {
		return false, fmt.Errorf("frames.MarkFrameEnded: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *framesImpl) MarkOpenFramesEndedForInstance(
	ctx context.Context, instanceID shared.UUID, tx persistence.Tx,
) (int, error) {
	res, err := s.q(tx).ExecContext(ctx, `
        UPDATE rimsky_frames
        SET ended_at = ?
        WHERE instance_id = ? AND ended_at IS NULL
    `, nowUTC(), instanceID.String())
	if err != nil {
		return 0, fmt.Errorf("frames.MarkOpenFramesEndedForInstance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

const frameEndStateCaseSQL = `CASE
    WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = ? AND r.state = 'failed'
                 AND (r.settling_signal_type IS NULL OR r.settling_signal_type <> '` + cascade.SettlingSignalInstanceKilled + `')) THEN 'failed'
    WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = ? AND r.state = 'failed'
                 AND r.settling_signal_type = '` + cascade.SettlingSignalInstanceKilled + `') THEN 'terminated'
    ELSE 'completed'
END`

func (s *framesImpl) EndFrameIfSettled(
	ctx context.Context, frameID shared.UUID, tx persistence.Tx,
) (persistence.FrameEndResult, error) {
	var startedAt, endedAt sql.NullString
	var finalState string
	err := s.q(tx).QueryRowContext(ctx, `
        UPDATE rimsky_frames
           SET ended_at = ?
         WHERE frame_id = ?
           AND ended_at IS NULL
           AND NOT EXISTS (
               SELECT 1 FROM rimsky_node_runs r
                WHERE r.frame_id = ?
                  AND r.state IN (`+inFlightNodeRunStates+`)
           )
        RETURNING started_at, ended_at, `+frameEndStateCaseSQL+`
    `, nowUTC(), frameID.String(), frameID.String(), frameID.String(), frameID.String()).Scan(&startedAt, &endedAt, &finalState)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistence.FrameEndResult{}, nil
		}
		return persistence.FrameEndResult{}, fmt.Errorf("frames.EndFrameIfSettled: %w", err)
	}
	res := persistence.FrameEndResult{Transitioned: true, FinalState: finalState}
	if res.StartedAt, err = parseNullableTime(startedAt); err != nil {
		return persistence.FrameEndResult{}, fmt.Errorf("frames.EndFrameIfSettled: started_at: %w", err)
	}
	if res.EndedAt, err = parseNullableTime(endedAt); err != nil {
		return persistence.FrameEndResult{}, fmt.Errorf("frames.EndFrameIfSettled: ended_at: %w", err)
	}
	return res, nil
}

const prunedFrameIDsSQL = `
    SELECT frame_id FROM (
        SELECT f.frame_id, f.ended_at,
               ROW_NUMBER() OVER (
                   PARTITION BY f.instance_id
                   ORDER BY COALESCE(f.ended_at, f.started_at) DESC, f.frame_id DESC
               ) AS rk
          FROM rimsky_frames f
         WHERE f.ended_at IS NOT NULL
    ) ranked
    WHERE ranked.rk > ?
       OR (ranked.ended_at IS NOT NULL AND ranked.ended_at < ?)
`

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
	ti := (*tablesImpl)(s)
	frameIDs, err := s.snapshotPrunableFrameIDs(ctx, countCap, cutoffArg)
	if err != nil {
		return 0, fmt.Errorf("frames.PruneTraceForRetention: %w", err)
	}
	if len(frameIDs) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(frameIDs)), ",")
	frameArgs := make([]any, len(frameIDs))
	for i, id := range frameIDs {
		frameArgs[i] = id
	}
	var affected int
	err = ti.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		scratchHandles, err := queuePrunedBlobHandles(ctx, ti, tx, `
            SELECT scratch_handle, scratch_handle_backend
              FROM rimsky_node_runs
             WHERE scratch_handle IS NOT NULL
               AND frame_id IN (`+placeholders+`)
        `, frameArgs...)
		if err != nil {
			return fmt.Errorf("select blob-backed scratch handles: %w", err)
		}
		attrHandles, err := queuePrunedBlobHandles(ctx, ti, tx, `
            SELECT a.value_handle, a.value_handle_backend
              FROM rimsky_node_attributes a
              JOIN rimsky_node_runs r ON r.id = a.node_run_id
             WHERE a.value_handle IS NOT NULL
               AND r.frame_id IN (`+placeholders+`)
        `, frameArgs...)
		if err != nil {
			return fmt.Errorf("select blob-backed attribute handles: %w", err)
		}
		now := time.Now().UTC()
		for _, h := range append(scratchHandles, attrHandles...) {
			if err := persistence.QueueBlobOrphan(ctx, ti.BlobOrphans(), tx, h.handle, h.backend, now, ti.BlobRetention()); err != nil {
				return fmt.Errorf("queue blob orphan %q: %w", h.handle, err)
			}
		}
		rootScopeRows, err := ti.q(tx).QueryContext(ctx, `
            SELECT root_run_scope_id
              FROM rimsky_frames
             WHERE frame_id IN (`+placeholders+`)
        `, frameArgs...)
		if err != nil {
			return fmt.Errorf("select pruned frames' root run scopes: %w", err)
		}
		var rootScopeIDs []string
		for rootScopeRows.Next() {
			var id string
			if scanErr := rootScopeRows.Scan(&id); scanErr != nil {
				rootScopeRows.Close()
				return fmt.Errorf("scan pruned frame's root run scope: %w", scanErr)
			}
			rootScopeIDs = append(rootScopeIDs, id)
		}
		rootScopeRows.Close()
		if err := rootScopeRows.Err(); err != nil {
			return fmt.Errorf("iterate pruned frames' root run scopes: %w", err)
		}
		res, err := ti.q(tx).ExecContext(ctx, `
            DELETE FROM rimsky_frames
             WHERE frame_id IN (`+placeholders+`)
        `, frameArgs...)
		if err != nil {
			return fmt.Errorf("delete pruned frames: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		affected = int(n)
		if len(rootScopeIDs) > 0 {
			scopePlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(rootScopeIDs)), ",")
			scopeArgs := make([]any, len(rootScopeIDs))
			for i, id := range rootScopeIDs {
				scopeArgs[i] = id
			}
			if _, err := ti.q(tx).ExecContext(ctx, `
                DELETE FROM rimsky_run_scopes
                 WHERE id IN (`+scopePlaceholders+`)
                   AND NOT EXISTS (SELECT 1 FROM rimsky_frames f WHERE f.root_run_scope_id = rimsky_run_scopes.id)
                   AND NOT EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.run_scope_id = rimsky_run_scopes.id)
            `, scopeArgs...); err != nil {
				return fmt.Errorf("delete pruned frames' root run scopes: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("frames.PruneTraceForRetention: %w", err)
	}
	return affected, nil
}

type prunedBlobHandle struct {
	handle  string
	backend string
}

func queuePrunedBlobHandles(
	ctx context.Context, ti *tablesImpl, tx persistence.Tx, sqlText string, args ...any,
) ([]prunedBlobHandle, error) {
	rows, err := ti.q(tx).QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []prunedBlobHandle
	for rows.Next() {
		var handle string
		var backend sql.NullString
		if err := rows.Scan(&handle, &backend); err != nil {
			return nil, fmt.Errorf("scan blob handle: %w", err)
		}
		out = append(out, prunedBlobHandle{handle: handle, backend: backend.String})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate blob handles: %w", err)
	}
	return out, nil
}

func (s *framesImpl) snapshotPrunableFrameIDs(ctx context.Context, countCap int, cutoffArg string) ([]string, error) {
	rows, err := (*tablesImpl)(s).db.QueryContext(ctx, prunedFrameIDsSQL, countCap, cutoffArg)
	if err != nil {
		return nil, fmt.Errorf("snapshot prunable frames: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan prunable frame id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prunable frame ids: %w", err)
	}
	return out, nil
}

func (s *framesImpl) GetRunningFrameID(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (*shared.UUID, error) {
	var frameIDStr string
	err := s.q(tx).QueryRowContext(ctx, `
        SELECT frame_id
          FROM rimsky_frames
         WHERE instance_id = ? AND ended_at IS NULL
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
                   JOIN json_each(nd.value, '$.claim_producers') AS store
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
                AND r.state IN (`+inFlightNodeRunStates+`)
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

func (s *framesImpl) ListOrphanFrameDispatches(ctx context.Context, tx persistence.Tx) ([]persistence.OrphanFrameDispatch, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
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

func (s *framesImpl) InsertRunningFrame(
	ctx context.Context, instanceID, triggeringMessageID, rootRunScopeID shared.UUID, tx persistence.Tx,
) (shared.UUID, error) {
	frameID := uuid.New()
	now := nowUTC()
	_, err := s.q(tx).ExecContext(ctx, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, triggering_message_id, root_run_scope_id, started_at, last_progress_at)
        VALUES (?, ?, ?, ?, ?, ?)
    `, frameID.String(), instanceID.String(), triggeringMessageID.String(), rootRunScopeID.String(),
		now, now)
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
	var instArg, unresolvedArg, triggerArg, terminalStateArg any
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.Unresolved != nil {
		if *filter.Unresolved {
			unresolvedArg = 1
		} else {
			unresolvedArg = 0
		}
	}
	if filter.TriggeringMessageID != nil {
		triggerArg = filter.TriggeringMessageID.String()
	}
	if filter.TerminalState != nil {
		terminalStateArg = *filter.TerminalState
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
		`SELECT f.frame_id, f.instance_id,
		        `+frameStateCaseSQL+` AS state,
		        f.triggering_message_id, f.root_run_scope_id, f.started_at, f.ended_at, f.last_progress_at
		   FROM rimsky_frames f
		  WHERE (? IS NULL OR f.instance_id = ?)
		    AND (? IS NULL OR (? = 1 AND f.ended_at IS NULL) OR (? = 0 AND f.ended_at IS NOT NULL))
		    AND (? IS NULL OR (f.started_at, f.frame_id) < (?, ?))
		    AND (? IS NULL OR f.triggering_message_id = ?)
		    AND (? IS NULL OR (`+frameStateCaseSQL+`) = ?)
		  ORDER BY f.started_at DESC, f.frame_id DESC
		  LIMIT ?`,
		instArg, instArg, unresolvedArg, unresolvedArg, unresolvedArg, cursorQAt, cursorQAt, cursorFid, triggerArg, triggerArg,
		terminalStateArg, terminalStateArg, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.FrameRow]{}, err
	}
	defer rows.Close()
	var out []persistence.FrameRow
	var lastStarted time.Time
	for rows.Next() {
		cols, err := scanFrameObservabilityCols(rows)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		r, err := cols.toFrameRow("frames.ListForObservability")
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRow]{}, err
		}
		if r.StartedAt != nil {
			lastStarted = *r.StartedAt
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.FrameRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = encodeFrameCursor(lastStarted, out[len(out)-1].FrameID)
	}
	return persistence.PaginatedListResult[persistence.FrameRow]{Rows: out, NextCursor: nextCursor}, nil
}

type frameCursor struct {
	StartedAt time.Time   `json:"s"`
	F         shared.UUID `json:"f"`
}

func encodeFrameCursor(started time.Time, fid shared.UUID) string {
	b, _ := json.Marshal(frameCursor{StartedAt: started, F: fid})
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
	return c.StartedAt, c.F, nil
}

func (s *framesImpl) CountHeldFrames(ctx context.Context, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
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

type frameObservabilityCols struct {
	fidStr         string
	iidStr         string
	state          string
	triggeringMsg  string
	rootScope      string
	startedAt      sql.NullString
	endedAt        sql.NullString
	lastProgressAt sql.NullString
}

func scanFrameObservabilityCols(sc scannable, extra ...any) (frameObservabilityCols, error) {
	var c frameObservabilityCols
	dest := append([]any{&c.fidStr, &c.iidStr, &c.state, &c.triggeringMsg, &c.rootScope, &c.startedAt, &c.endedAt, &c.lastProgressAt}, extra...)
	err := sc.Scan(dest...)
	return c, err
}

func (c frameObservabilityCols) toFrameRow(caller string) (persistence.FrameRow, error) {
	var r persistence.FrameRow
	fid, err := uuid.Parse(c.fidStr)
	if err != nil {
		return persistence.FrameRow{}, err
	}
	iid, err := uuid.Parse(c.iidStr)
	if err != nil {
		return persistence.FrameRow{}, err
	}
	mid, err := uuid.Parse(c.triggeringMsg)
	if err != nil {
		return persistence.FrameRow{}, err
	}
	r.FrameID = fid
	r.InstanceID = iid
	r.State = c.state
	r.TriggeringMessageID = mid
	if c.rootScope != "" {
		rsid, err := uuid.Parse(c.rootScope)
		if err != nil {
			return persistence.FrameRow{}, fmt.Errorf("%s: root_run_scope_id: %w", caller, err)
		}
		r.RootRunScopeID = rsid
	}
	if r.StartedAt, err = parseNullableTime(c.startedAt); err != nil {
		return persistence.FrameRow{}, fmt.Errorf("%s: started_at: %w", caller, err)
	}
	if r.EndedAt, err = parseNullableTime(c.endedAt); err != nil {
		return persistence.FrameRow{}, fmt.Errorf("%s: ended_at: %w", caller, err)
	}
	if r.LastProgressAt, err = parseNullableTime(c.lastProgressAt); err != nil {
		return persistence.FrameRow{}, fmt.Errorf("%s: last_progress_at: %w", caller, err)
	}
	return r, nil
}

func (s *framesImpl) GetForObservability(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT f.frame_id, f.instance_id,
		        `+frameStateCaseSQL+` AS state,
		        f.triggering_message_id, f.root_run_scope_id, f.started_at, f.ended_at, f.last_progress_at
		   FROM rimsky_frames f WHERE f.frame_id = ?`,
		frameID.String(),
	)
	cols, err := scanFrameObservabilityCols(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r, err := cols.toFrameRow("frames.GetForObservability")
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *framesImpl) GetForObservabilityWithMessage(ctx context.Context, frameID shared.UUID, tx persistence.Tx) (*persistence.FrameRowWithMessage, error) {
	var mType, mSender, mKind sql.NullString
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT f.frame_id, f.instance_id,
		        `+frameStateCaseSQL+` AS state,
		        f.triggering_message_id, f.root_run_scope_id,
		        f.started_at, f.ended_at, f.last_progress_at,
		        m.type, m.sender, m.sender_kind
		   FROM rimsky_frames f
		   LEFT JOIN rimsky_messages m ON m.id = f.triggering_message_id
		  WHERE f.frame_id = ?`,
		frameID.String(),
	)
	cols, err := scanFrameObservabilityCols(row, &mType, &mSender, &mKind)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	base, err := cols.toFrameRow("frames.GetForObservabilityWithMessage")
	if err != nil {
		return nil, err
	}
	r := persistence.FrameRowWithMessage{FrameRow: base}
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
	var instArg, unresolvedArg, triggerArg, terminalStateArg any
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.Unresolved != nil {
		if *filter.Unresolved {
			unresolvedArg = 1
		} else {
			unresolvedArg = 0
		}
	}
	if filter.TriggeringMessageID != nil {
		triggerArg = filter.TriggeringMessageID.String()
	}
	if filter.TerminalState != nil {
		terminalStateArg = *filter.TerminalState
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
		`SELECT f.frame_id, f.instance_id,
		        `+frameStateCaseSQL+` AS state,
		        f.triggering_message_id, f.root_run_scope_id,
		        f.started_at, f.ended_at, f.last_progress_at,
		        m.type, m.sender, m.sender_kind
		   FROM rimsky_frames f
		   LEFT JOIN rimsky_messages m ON m.id = f.triggering_message_id
		  WHERE (? IS NULL OR f.instance_id = ?)
		    AND (? IS NULL OR (? = 1 AND f.ended_at IS NULL) OR (? = 0 AND f.ended_at IS NOT NULL))
		    AND (? IS NULL OR (f.started_at, f.frame_id) < (?, ?))
		    AND (? IS NULL OR f.triggering_message_id = ?)
		    AND (? IS NULL OR (`+frameStateCaseSQL+`) = ?)
		  ORDER BY f.started_at DESC, f.frame_id DESC
		  LIMIT ?`,
		instArg, instArg, unresolvedArg, unresolvedArg, unresolvedArg, cursorQAt, cursorQAt, cursorFid, triggerArg, triggerArg,
		terminalStateArg, terminalStateArg, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
	}
	defer rows.Close()
	var out []persistence.FrameRowWithMessage
	var lastStarted time.Time
	for rows.Next() {
		var mType, mSender, mKind sql.NullString
		cols, err := scanFrameObservabilityCols(rows, &mType, &mSender, &mKind)
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
		}
		base, err := cols.toFrameRow("frames.ListForObservabilityWithMessage")
		if err != nil {
			return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
		}
		r := persistence.FrameRowWithMessage{FrameRow: base}
		if mType.Valid {
			r.MessageType = mType.String
		}
		if mSender.Valid {
			r.MessageSender = mSender.String
		}
		if mKind.Valid {
			r.MessageSenderKind = mKind.String
		}
		if r.StartedAt != nil {
			lastStarted = *r.StartedAt
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = encodeFrameCursor(lastStarted, out[len(out)-1].FrameID)
	}
	return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{Rows: out, NextCursor: nextCursor}, nil
}
