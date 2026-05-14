// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// frames.go — SQLite-backed persistence.FrameTable.
//
// SQLite stores `source_node_ids` as a JSON array of UUID strings (TEXT).
// Append/contains operations use json_each / json_array helpers.
//
// `frame_resolution` and `frame_timeout_ms` are read out of the
// rimsky_templates.spec TEXT (JSON) column via json_extract.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

func (s *framesImpl) ListRunningFramesNoPendingNodes(ctx context.Context, tx persistence.Tx) ([]persistence.FramePending, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
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
            SELECT 1 FROM rimsky_nodes n
            WHERE n.instance_id = ?
              AND n.frame_id = ?
              AND n.state = 'failed'
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

func (s *framesImpl) MarkInstanceTerminatedIfDone(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx, `
        UPDATE rimsky_instances
        SET terminated_at = ?
        WHERE id = ?
          AND terminated_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_frames f
              WHERE f.instance_id = rimsky_instances.id AND f.state IN ('queued','running')
          )
          AND NOT EXISTS (
              SELECT 1 FROM rimsky_nodes n
              WHERE n.instance_id = rimsky_instances.id AND n.state IN ('stale','running')
          )
    `, nowUTC(), instanceID.String())
	if err != nil {
		return fmt.Errorf("frames.MarkInstanceTerminatedIfDone: %w", err)
	}
	return nil
}

// ListQueuedFramesReadyToStart returns at most one queued frame per
// instance — the oldest queued — for instances that have no
// currently-running frame.
//
// SQLite has no DISTINCT ON; we emulate via row_number() OVER (PARTITION BY).
func (s *framesImpl) ListQueuedFramesReadyToStart(ctx context.Context, tx persistence.Tx) ([]persistence.FrameQueuedReady, error) {
	rows, err := s.q(tx).QueryContext(ctx, `
        WITH ranked AS (
            SELECT f.frame_id, f.instance_id, f.source_node_ids,
                   ROW_NUMBER() OVER (PARTITION BY f.instance_id ORDER BY f.queued_at ASC) AS rn
            FROM rimsky_frames f
            WHERE f.state = 'queued'
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

func (s *framesImpl) MarkSourceNodeStale(
	ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx persistence.Tx,
) (bool, error) {
	res, err := s.q(tx).ExecContext(ctx, `
        UPDATE rimsky_nodes
        SET state = 'stale', frame_id = ?, updated_at = ?
        WHERE instance_id = ? AND id = ?
          AND (state IN ('fresh','failed')
               OR (state = 'stale' AND frame_id IS NULL))
    `, frameID.String(), nowUTC(), instanceID.String(), nodeID.String())
	if err != nil {
		return false, fmt.Errorf("frames.MarkSourceNodeStale: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
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
              SELECT 1 FROM rimsky_nodes n
              WHERE n.instance_id = f.instance_id AND n.state IN ('stale','running')
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
		// Per the reactive-loops + lifecycle-handlers spec §7,
		// frame_timeout_ms measures "no progress in window" rather than
		// frame age. Compare against last_progress_at (refreshed by every
		// node-state transition write) instead of started_at.
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
	// Explicitly write last_progress_at at insert time using nowUTC()
	// (RFC3339Nano) so the column is uniformly nano-precision across
	// all rows. The migration's strftime DEFAULT only delivers
	// millisecond precision; relying on it for runtime inserts would
	// leave the column with mixed precision and break any future
	// SQL-level string comparison against the column.
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
	// Look for an existing queued+coalesce frame for this instance under
	// the surrounding tx.
	var existing string
	err := s.q(tx).QueryRowContext(ctx, `
        SELECT frame_id FROM rimsky_frames
        WHERE instance_id = ? AND state = 'queued' AND frame_resolution_mode = 'coalesce'
        LIMIT 1
    `, instanceID.String()).Scan(&existing)
	if err == nil {
		// Append sourceNodeID if not already present.
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

	// Insert a new coalesce frame. Write last_progress_at explicitly
	// (see EnqueueSerialFrame for the precision rationale).
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
