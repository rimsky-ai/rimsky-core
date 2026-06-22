// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type breakpointHitsImpl tablesImpl

var _ persistence.BreakpointHitTable = (*breakpointHitsImpl)(nil)

func (s *tablesImpl) BreakpointHits() persistence.BreakpointHitTable {
	return (*breakpointHitsImpl)(s)
}

func (b *breakpointHitsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteBreakpointHitCols = `seq, id, breakpoint_id, instance_id, node_run_id, frame_id,
	checkpoint, mode, snapshot, hit_at, resumed_at, resumed_by_key, resume_overlay`

func (b *breakpointHitsImpl) Create(ctx context.Context, hit persistence.BreakpointHitRow, tx persistence.Tx) (shared.UUID, int64, error) {
	ex := b.q(tx)
	id := hit.ID
	if id == (shared.UUID{}) {
		id = uuid.New()
	}
	snapshot := hit.Snapshot
	if snapshot == nil {
		snapshot = map[string]any{}
	}
	snapBytes, err := json.Marshal(snapshot)
	if err != nil {
		return shared.UUID{}, 0, fmt.Errorf("sqlite.breakpointHits.create: marshal snapshot: %w", err)
	}
	hitAt := time.Now().UTC().Format(timeLayoutFixedNanos)
	var nodeRunArg, frameArg any
	if hit.NodeRunID != nil {
		nodeRunArg = hit.NodeRunID.String()
	}
	if hit.FrameID != nil {
		frameArg = hit.FrameID.String()
	}
	var (
		idStr string
		seq   int64
	)
	row := ex.QueryRowContext(ctx,
		`INSERT INTO rimsky_breakpoint_hits
		   (id, breakpoint_id, instance_id, node_run_id, frame_id,
		    checkpoint, mode, snapshot, hit_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id, seq`,
		id.String(), hit.BreakpointID.String(), hit.InstanceID.String(),
		nodeRunArg, frameArg,
		string(hit.Checkpoint), string(hit.Mode), string(snapBytes), hitAt,
	)
	if err := row.Scan(&idStr, &seq); err != nil {
		return shared.UUID{}, 0, fmt.Errorf("sqlite.breakpointHits.create: %w", err)
	}
	outID, err := uuid.Parse(idStr)
	if err != nil {
		return shared.UUID{}, 0, fmt.Errorf("sqlite.breakpointHits.create.parseID: %w", err)
	}
	return outID, seq, nil
}

func (b *breakpointHitsImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.BreakpointHitRow, error) {
	ex := b.q(tx)
	row := ex.QueryRowContext(ctx,
		`SELECT `+sqliteBreakpointHitCols+`
		   FROM rimsky_breakpoint_hits
		  WHERE id = ?`, id.String())
	out, err := scanSqliteBreakpointHit(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite.breakpointHits.get: %w", err)
	}
	return &out, nil
}

func (b *breakpointHitsImpl) ListSinceForInstance(ctx context.Context, instanceID shared.UUID, sinceSeq int64, limit int, tx persistence.Tx) ([]persistence.BreakpointHitRow, error) {
	ex := b.q(tx)
	if limit <= 0 {
		limit = 100
	}
	rows, err := ex.QueryContext(ctx,
		`SELECT `+sqliteBreakpointHitCols+`
		   FROM rimsky_breakpoint_hits
		  WHERE instance_id = ? AND seq > ?
		  ORDER BY seq ASC
		  LIMIT ?`,
		instanceID.String(), sinceSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite.breakpointHits.listSinceForInstance: %w", err)
	}
	return scanSqliteBreakpointHits(rows)
}

func (b *breakpointHitsImpl) ListSinceForBreakpoint(ctx context.Context, bpID shared.UUID, sinceSeq int64, limit int, tx persistence.Tx) ([]persistence.BreakpointHitRow, error) {
	ex := b.q(tx)
	if limit <= 0 {
		limit = 100
	}
	rows, err := ex.QueryContext(ctx,
		`SELECT `+sqliteBreakpointHitCols+`
		   FROM rimsky_breakpoint_hits
		  WHERE breakpoint_id = ? AND seq > ?
		  ORDER BY seq ASC
		  LIMIT ?`,
		bpID.String(), sinceSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite.breakpointHits.listSinceForBreakpoint: %w", err)
	}
	return scanSqliteBreakpointHits(rows)
}

func (b *breakpointHitsImpl) ListUnresumedForBreakpoint(ctx context.Context, bpID shared.UUID, tx persistence.Tx) ([]persistence.BreakpointHitRow, error) {
	ex := b.q(tx)
	rows, err := ex.QueryContext(ctx,
		`SELECT `+sqliteBreakpointHitCols+`
		   FROM rimsky_breakpoint_hits
		  WHERE breakpoint_id = ? AND resumed_at IS NULL
		  ORDER BY hit_at ASC`,
		bpID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.breakpointHits.listUnresumedForBreakpoint: %w", err)
	}
	return scanSqliteBreakpointHits(rows)
}

func (b *breakpointHitsImpl) Resume(ctx context.Context, id shared.UUID, byKey string, overlay map[string]any, tx persistence.Tx) error {
	ex := b.q(tx)
	var overlayArg any
	if overlay != nil {
		bb, err := json.Marshal(overlay)
		if err != nil {
			return fmt.Errorf("sqlite.breakpointHits.resume: marshal overlay: %w", err)
		}
		overlayArg = string(bb)
	}
	nowStr := time.Now().UTC().Format(timeLayoutFixedNanos)
	res, err := ex.ExecContext(ctx,
		`UPDATE rimsky_breakpoint_hits
		    SET resumed_at = ?, resumed_by_key = ?, resume_overlay = ?
		  WHERE id = ? AND resumed_at IS NULL`,
		nowStr, byKey, overlayArg, id.String())
	if err != nil {
		return fmt.Errorf("sqlite.breakpointHits.resume: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.breakpointHits.resume.rowsAffected: %w", err)
	}
	if n == 1 {
		return nil
	}
	var resumedAt sql.NullString
	err = ex.QueryRowContext(ctx,
		`SELECT resumed_at FROM rimsky_breakpoint_hits WHERE id = ?`, id.String()).Scan(&resumedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return shared.ErrBreakpointHitNotFound
		}
		return fmt.Errorf("sqlite.breakpointHits.resume.probe: %w", err)
	}
	return nil
}

func (b *breakpointHitsImpl) AutoResumeStale(ctx context.Context, now time.Time, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	nowStr := now.UTC().Format(timeLayoutFixedNanos)
	res, err := ex.ExecContext(ctx,
		`UPDATE rimsky_breakpoint_hits
		    SET resumed_at = ?, resumed_by_key = 'sweeper'
		  WHERE resumed_at IS NULL
		    AND seq IN (
		        SELECT h.seq
		          FROM rimsky_breakpoint_hits h
		          JOIN rimsky_instance_breakpoints b
		            ON h.breakpoint_id = b.id
		         WHERE h.resumed_at IS NULL
		           AND b.overflow_policy = 'auto_resume_after_ttl'
		           AND datetime(h.hit_at, '+' || b.hit_ttl_seconds || ' seconds') <= datetime(?)
		    )`,
		nowStr, nowStr)
	if err != nil {
		return 0, fmt.Errorf("sqlite.breakpointHits.autoResumeStale: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite.breakpointHits.autoResumeStale.rowsAffected: %w", err)
	}
	return int(n), nil
}

func (b *breakpointHitsImpl) DropOldest(ctx context.Context, bpID shared.UUID, keepCount int, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	if keepCount < 0 {
		keepCount = 0
	}
	res, err := ex.ExecContext(ctx,
		`DELETE FROM rimsky_breakpoint_hits
		  WHERE seq IN (
		      SELECT seq FROM rimsky_breakpoint_hits
		       WHERE breakpoint_id = ? AND resumed_at IS NULL
		       ORDER BY seq ASC
		       LIMIT MAX(0, (
		         SELECT COUNT(*) FROM rimsky_breakpoint_hits
		          WHERE breakpoint_id = ? AND resumed_at IS NULL
		       ) - ?)
		  )`,
		bpID.String(), bpID.String(), keepCount)
	if err != nil {
		return 0, fmt.Errorf("sqlite.breakpointHits.dropOldest: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite.breakpointHits.dropOldest.rowsAffected: %w", err)
	}
	return int(n), nil
}

func (b *breakpointHitsImpl) SweepOrphanedUnresumed(ctx context.Context, cutoff time.Time, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	cutoffStr := cutoff.UTC().Format(timeLayoutFixedNanos)
	res, err := ex.ExecContext(ctx,
		`DELETE FROM rimsky_breakpoint_hits
		  WHERE seq IN (
		      SELECT h.seq
		        FROM rimsky_breakpoint_hits h
		        JOIN rimsky_instance_breakpoints b
		          ON h.breakpoint_id = b.id
		       WHERE h.resumed_at IS NULL
		         AND b.overflow_policy <> 'auto_resume_after_ttl'
		         AND h.hit_at <= ?
		  )`,
		cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("sqlite.breakpointHits.sweepOrphanedUnresumed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite.breakpointHits.sweepOrphanedUnresumed.rowsAffected: %w", err)
	}
	return int(n), nil
}

func (b *breakpointHitsImpl) UnresumedCount(ctx context.Context, bpID shared.UUID, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	var n int
	if err := ex.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_breakpoint_hits
		  WHERE breakpoint_id = ? AND resumed_at IS NULL`, bpID.String()).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite.breakpointHits.unresumedCount: %w", err)
	}
	return n, nil
}

// @concept: breakpoint
func (b *breakpointHitsImpl) HasUnresumedPauseHitForInstance(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (bool, error) {
	ex := b.q(tx)
	var n int
	if err := ex.QueryRowContext(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM rimsky_breakpoint_hits AS h
		     JOIN rimsky_node_runs AS r ON r.id = h.node_run_id
		    WHERE h.instance_id = ?
		      AND h.mode = ?
		      AND h.resumed_at IS NULL
		      AND h.node_run_id IS NOT NULL
		      AND r.state IN ('pending','stale','running','held','parked')
		 )`, instanceID.String(), string(persistence.BreakpointModePause)).Scan(&n); err != nil {
		return false, fmt.Errorf("sqlite.breakpointHits.hasUnresumedPauseHitForInstance: %w", err)
	}
	return n != 0, nil
}

func scanSqliteBreakpointHits(rows *sql.Rows) ([]persistence.BreakpointHitRow, error) {
	defer rows.Close()
	out := []persistence.BreakpointHitRow{}
	for rows.Next() {
		r, err := scanSqliteBreakpointHit(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite.breakpointHits.scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanSqliteBreakpointHit(sc scannable) (persistence.BreakpointHitRow, error) {
	var (
		seq           int64
		idStr         string
		bpIDStr       string
		instanceIDStr string
		nodeRunIDStr  sql.NullString
		frameIDStr    sql.NullString
		checkpoint    string
		mode          string
		snapshotStr   string
		hitAtStr      string
		resumedAtStr  sql.NullString
		resumedByKey  sql.NullString
		resumeOverlay sql.NullString
	)
	if err := sc.Scan(&seq, &idStr, &bpIDStr, &instanceIDStr, &nodeRunIDStr, &frameIDStr,
		&checkpoint, &mode, &snapshotStr, &hitAtStr, &resumedAtStr, &resumedByKey, &resumeOverlay); err != nil {
		return persistence.BreakpointHitRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.BreakpointHitRow{}, fmt.Errorf("scan hit id: %w", err)
	}
	bpID, err := uuid.Parse(bpIDStr)
	if err != nil {
		return persistence.BreakpointHitRow{}, fmt.Errorf("scan hit breakpoint_id: %w", err)
	}
	instID, err := uuid.Parse(instanceIDStr)
	if err != nil {
		return persistence.BreakpointHitRow{}, fmt.Errorf("scan hit instance_id: %w", err)
	}
	hitAt, err := parseTime(hitAtStr)
	if err != nil {
		return persistence.BreakpointHitRow{}, fmt.Errorf("scan hit hit_at: %w", err)
	}
	snapshot := map[string]any{}
	if snapshotStr != "" {
		if err := json.Unmarshal([]byte(snapshotStr), &snapshot); err != nil {
			return persistence.BreakpointHitRow{}, fmt.Errorf("unmarshal snapshot: %w", err)
		}
	}
	out := persistence.BreakpointHitRow{
		Seq:          seq,
		ID:           id,
		BreakpointID: bpID,
		InstanceID:   instID,
		Checkpoint:   persistence.BreakpointCheckpoint(checkpoint),
		Mode:         persistence.BreakpointMode(mode),
		Snapshot:     snapshot,
		HitAt:        hitAt,
	}
	if nodeRunIDStr.Valid {
		u, err := uuid.Parse(nodeRunIDStr.String)
		if err != nil {
			return persistence.BreakpointHitRow{}, fmt.Errorf("scan hit node_run_id: %w", err)
		}
		out.NodeRunID = &u
	}
	if frameIDStr.Valid {
		u, err := uuid.Parse(frameIDStr.String)
		if err != nil {
			return persistence.BreakpointHitRow{}, fmt.Errorf("scan hit frame_id: %w", err)
		}
		out.FrameID = &u
	}
	if resumedAtStr.Valid {
		t, err := parseTime(resumedAtStr.String)
		if err != nil {
			return persistence.BreakpointHitRow{}, fmt.Errorf("scan hit resumed_at: %w", err)
		}
		out.ResumedAt = &t
	}
	if resumedByKey.Valid {
		s := resumedByKey.String
		out.ResumedByKey = &s
	}
	if resumeOverlay.Valid && resumeOverlay.String != "" {
		m := map[string]any{}
		if err := json.Unmarshal([]byte(resumeOverlay.String), &m); err != nil {
			return persistence.BreakpointHitRow{}, fmt.Errorf("unmarshal resume_overlay: %w", err)
		}
		out.ResumeOverlay = m
	}
	return out, nil
}
