// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// breakpointHitsImpl is the Postgres-backed persistence.BreakpointHitTable
// — append-only ledger of breakpoint matches per concept:breakpoint.
// Aspect-type pattern: per-row-type slice of *tablesImpl exposing the
// BreakpointHitTable method set, same shape as breakpointsImpl above.
type breakpointHitsImpl tablesImpl

var _ persistence.BreakpointHitTable = (*breakpointHitsImpl)(nil)

// BreakpointHits returns the postgres BreakpointHitTable impl.
func (s *tablesImpl) BreakpointHits() persistence.BreakpointHitTable {
	return (*breakpointHitsImpl)(s)
}

func (b *breakpointHitsImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

// breakpointHitCols is the canonical column list. seq is first because
// SELECT/RETURNING consumers always need both seq and id together
// (the spec-defined cursor + stable resume identity).
const breakpointHitCols = `seq, id, breakpoint_id, instance_id, node_run_id, frame_id,
	checkpoint, mode, snapshot, hit_at, resumed_at, resumed_by_key, resume_overlay`

// Create inserts a new rimsky_breakpoint_hits row. Returns both the
// UUID (stable identity for resume) and the int64 seq (monotonic cursor
// for resources/read pagination). Marshals snapshot map → JSONB.
func (b *breakpointHitsImpl) Create(ctx context.Context, hit persistence.BreakpointHitRow, tx persistence.Tx) (shared.UUID, int64, error) {
	ex := b.q(tx)
	snapshot := hit.Snapshot
	if snapshot == nil {
		snapshot = map[string]any{}
	}
	snapBytes, err := json.Marshal(snapshot)
	if err != nil {
		return shared.UUID{}, 0, fmt.Errorf("breakpointHits.create: marshal snapshot: %w", err)
	}
	var idArg any
	if hit.ID != (shared.UUID{}) {
		idArg = hit.ID
	}
	var nodeRunArg any
	if hit.NodeRunID != nil {
		nodeRunArg = *hit.NodeRunID
	}
	var frameArg any
	if hit.FrameID != nil {
		frameArg = *hit.FrameID
	}
	var (
		id  shared.UUID
		seq int64
	)
	err = ex.QueryRow(ctx,
		`INSERT INTO rimsky_breakpoint_hits
		   (id, breakpoint_id, instance_id, node_run_id, frame_id,
		    checkpoint, mode, snapshot, hit_at)
		 VALUES (
		    COALESCE($1::uuid, gen_random_uuid()),
		    $2, $3, $4, $5,
		    $6, $7, $8, NOW()
		 )
		 RETURNING id, seq`,
		idArg, hit.BreakpointID, hit.InstanceID, nodeRunArg, frameArg,
		string(hit.Checkpoint), string(hit.Mode), snapBytes,
	).Scan(&id, &seq)
	if err != nil {
		return shared.UUID{}, 0, fmt.Errorf("breakpointHits.create: %w", err)
	}
	return id, seq, nil
}

// Get returns the hit by id (the UUID column, not seq). Returns
// (nil, nil) on not-found.
func (b *breakpointHitsImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.BreakpointHitRow, error) {
	ex := b.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+breakpointHitCols+`
		   FROM rimsky_breakpoint_hits
		  WHERE id = $1`, id)
	out, err := scanBreakpointHit(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("breakpointHits.get: %w", err)
	}
	return &out, nil
}

// ListSinceForInstance pages forward through rimsky_breakpoint_hits by
// seq. INCLUDES resumed rows — the cursor pages through every hit; the
// agent inspects `resumed_at` per row to know its state.
func (b *breakpointHitsImpl) ListSinceForInstance(ctx context.Context, instanceID shared.UUID, sinceSeq int64, limit int, tx persistence.Tx) ([]persistence.BreakpointHitRow, error) {
	ex := b.q(tx)
	if limit <= 0 {
		limit = 100
	}
	rows, err := ex.Query(ctx,
		`SELECT `+breakpointHitCols+`
		   FROM rimsky_breakpoint_hits
		  WHERE instance_id = $1 AND seq > $2
		  ORDER BY seq ASC
		  LIMIT $3`,
		instanceID, sinceSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("breakpointHits.listSinceForInstance: %w", err)
	}
	return scanBreakpointHits(rows)
}

// ListSinceForBreakpoint mirrors ListSinceForInstance but filtered by
// breakpoint_id.
func (b *breakpointHitsImpl) ListSinceForBreakpoint(ctx context.Context, bpID shared.UUID, sinceSeq int64, limit int, tx persistence.Tx) ([]persistence.BreakpointHitRow, error) {
	ex := b.q(tx)
	if limit <= 0 {
		limit = 100
	}
	rows, err := ex.Query(ctx,
		`SELECT `+breakpointHitCols+`
		   FROM rimsky_breakpoint_hits
		  WHERE breakpoint_id = $1 AND seq > $2
		  ORDER BY seq ASC
		  LIMIT $3`,
		bpID, sinceSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("breakpointHits.listSinceForBreakpoint: %w", err)
	}
	return scanBreakpointHits(rows)
}

// ListUnresumedForBreakpoint returns hits with resumed_at IS NULL,
// oldest first. Drives the drop_oldest and block_dispatch overflow
// policies.
func (b *breakpointHitsImpl) ListUnresumedForBreakpoint(ctx context.Context, bpID shared.UUID, tx persistence.Tx) ([]persistence.BreakpointHitRow, error) {
	ex := b.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+breakpointHitCols+`
		   FROM rimsky_breakpoint_hits
		  WHERE breakpoint_id = $1 AND resumed_at IS NULL
		  ORDER BY hit_at ASC`,
		bpID)
	if err != nil {
		return nil, fmt.Errorf("breakpointHits.listUnresumedForBreakpoint: %w", err)
	}
	return scanBreakpointHits(rows)
}

// Resume sets resumed_at = NOW() (idempotently — replay returns nil).
// On 0 rows affected, probes the row: if it exists with resumed_at
// already set → idempotent replay → nil; if it doesn't exist →
// shared.ErrBreakpointHitNotFound.
func (b *breakpointHitsImpl) Resume(ctx context.Context, id shared.UUID, byKey string, overlay map[string]any, tx persistence.Tx) error {
	ex := b.q(tx)
	var overlayBytes []byte
	if overlay != nil {
		bb, err := json.Marshal(overlay)
		if err != nil {
			return fmt.Errorf("breakpointHits.resume: marshal overlay: %w", err)
		}
		overlayBytes = bb
	}
	var overlayArg any
	if overlayBytes != nil {
		overlayArg = overlayBytes
	}
	tag, err := ex.Exec(ctx,
		`UPDATE rimsky_breakpoint_hits
		    SET resumed_at = NOW(), resumed_by_key = $2, resume_overlay = $3
		  WHERE id = $1 AND resumed_at IS NULL`,
		id, byKey, overlayArg)
	if err != nil {
		return fmt.Errorf("breakpointHits.resume: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// @deliberate: zero rows means either the row is already resumed (replay → nil)
	// or it doesn't exist (→ ErrBreakpointHitNotFound); probe to distinguish.
	var resumedAt *time.Time
	err = ex.QueryRow(ctx,
		`SELECT resumed_at FROM rimsky_breakpoint_hits WHERE id = $1`, id).Scan(&resumedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shared.ErrBreakpointHitNotFound
		}
		return fmt.Errorf("breakpointHits.resume.probe: %w", err)
	}
	// @deliberate: row exists and resumed_at IS NOT NULL implies replay; a NULL
	// resumed_at here would have been caught by the UPDATE above, so surface as
	// an idempotent no-op rather than an error.
	_ = resumedAt
	return nil
}

// AutoResumeStale resumes every unresumed hit whose breakpoint's
// overflow_policy = 'auto_resume_after_ttl' and whose hit_at + hit_ttl
// has passed. Returns the rowcount.
func (b *breakpointHitsImpl) AutoResumeStale(ctx context.Context, now time.Time, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	tag, err := ex.Exec(ctx,
		`UPDATE rimsky_breakpoint_hits h
		    SET resumed_at = $1, resumed_by_key = 'sweeper'
		   FROM rimsky_instance_breakpoints b
		  WHERE h.breakpoint_id = b.id
		    AND h.resumed_at IS NULL
		    AND b.overflow_policy = 'auto_resume_after_ttl'
		    AND h.hit_at + (b.hit_ttl_seconds || ' seconds')::interval <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("breakpointHits.autoResumeStale: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DropOldest deletes the oldest unresumed hits beyond keepCount for
// the breakpoint. Returns the number of rows actually deleted.
func (b *breakpointHitsImpl) DropOldest(ctx context.Context, bpID shared.UUID, keepCount int, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	if keepCount < 0 {
		keepCount = 0
	}
	tag, err := ex.Exec(ctx,
		`DELETE FROM rimsky_breakpoint_hits
		  WHERE seq IN (
		      SELECT seq FROM rimsky_breakpoint_hits
		       WHERE breakpoint_id = $1 AND resumed_at IS NULL
		       ORDER BY seq ASC
		       LIMIT GREATEST(0, (
		         SELECT COUNT(*) FROM rimsky_breakpoint_hits
		          WHERE breakpoint_id = $1 AND resumed_at IS NULL
		       ) - $2)
		  )`,
		bpID, keepCount)
	if err != nil {
		return 0, fmt.Errorf("breakpointHits.dropOldest: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SweepOrphanedUnresumed deletes unresumed hits older than `cutoff`
// whose parent breakpoint's overflow_policy is NOT
// `auto_resume_after_ttl` (the AutoResumeStale path owns that case
// because the per-breakpoint hit_ttl drives it). Returns the rowcount.
//
// Purpose: reap rows abandoned mid-block when the supervisor crashes
// or context-cancels in `runtime.handleOverflow` / `waitForResume` —
// the persisted hit row stays unresumed but no waiter ever returns,
// so it would accumulate across restarts under load.
func (b *breakpointHitsImpl) SweepOrphanedUnresumed(ctx context.Context, cutoff time.Time, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	tag, err := ex.Exec(ctx,
		`DELETE FROM rimsky_breakpoint_hits h
		   USING rimsky_instance_breakpoints b
		  WHERE h.breakpoint_id = b.id
		    AND h.resumed_at IS NULL
		    AND b.overflow_policy <> 'auto_resume_after_ttl'
		    AND h.hit_at <= $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("breakpointHits.sweepOrphanedUnresumed: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// UnresumedCount returns the number of unresumed hits for the breakpoint.
func (b *breakpointHitsImpl) UnresumedCount(ctx context.Context, bpID shared.UUID, tx persistence.Tx) (int, error) {
	ex := b.q(tx)
	var n int
	if err := ex.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_breakpoint_hits
		  WHERE breakpoint_id = $1 AND resumed_at IS NULL`, bpID).Scan(&n); err != nil {
		return 0, fmt.Errorf("breakpointHits.unresumedCount: %w", err)
	}
	return n, nil
}

// HasUnresumedPauseHitForInstance reports whether the instance has at
// least one pause-mode breakpoint hit that is currently BLOCKING a
// runner — i.e. an unresumed hit whose `node_run_id` points at a
// node-run row still in a non-terminal phase
// (pending/active/held/parked). Drives the debug-channel gate; runs
// inside the request tx so the gate-check and the override mutation
// share one snapshot.
//
// The phase-non-terminal join is what makes the predicate match the
// `concept:breakpoint` and STORY-debug-channel language ("blocking a
// runner"). A stale hit row whose runner died and whose node-run
// transitioned to phase=completed or phase=failed before heartbeat
// reclamation cleaned the hit up is NOT a blocker — admitting it
// would let the override fire while there is no actual debugger
// session to overlay onto. A hit with node_run_id IS NULL (defensive;
// the supervisor populates it when the hit fires) is similarly
// excluded — without a target node-run we cannot establish that a
// runner is parked.
//
// @concept: breakpoint
func (b *breakpointHitsImpl) HasUnresumedPauseHitForInstance(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (bool, error) {
	ex := b.q(tx)
	var exists bool
	if err := ex.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM rimsky_breakpoint_hits AS h
		     JOIN rimsky_node_runs AS r ON r.id = h.node_run_id
		    WHERE h.instance_id = $1
		      AND h.mode = $2
		      AND h.resumed_at IS NULL
		      AND h.node_run_id IS NOT NULL
		      AND r.phase IN ('pending','active','held','parked')
		 )`, instanceID, string(persistence.BreakpointModePause)).Scan(&exists); err != nil {
		return false, fmt.Errorf("breakpointHits.hasUnresumedPauseHitForInstance: %w", err)
	}
	return exists, nil
}

func scanBreakpointHits(rows pgx.Rows) ([]persistence.BreakpointHitRow, error) {
	defer rows.Close()
	out := []persistence.BreakpointHitRow{}
	for rows.Next() {
		r, err := scanBreakpointHit(rows)
		if err != nil {
			return nil, fmt.Errorf("breakpointHits.scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanBreakpointHit(sc scannable) (persistence.BreakpointHitRow, error) {
	var (
		seq          int64
		id           shared.UUID
		bpID         shared.UUID
		instanceID   shared.UUID
		nodeRunID    *shared.UUID
		frameID      *shared.UUID
		checkpoint   string
		mode         string
		snapshotJSON []byte
		hitAt        time.Time
		resumedAt    *time.Time
		resumedByKey *string
		overlayJSON  []byte
	)
	if err := sc.Scan(&seq, &id, &bpID, &instanceID, &nodeRunID, &frameID,
		&checkpoint, &mode, &snapshotJSON, &hitAt, &resumedAt, &resumedByKey, &overlayJSON); err != nil {
		return persistence.BreakpointHitRow{}, err
	}
	snapshot := map[string]any{}
	if len(snapshotJSON) > 0 {
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return persistence.BreakpointHitRow{}, fmt.Errorf("unmarshal snapshot: %w", err)
		}
	}
	var overlay map[string]any
	if len(overlayJSON) > 0 {
		overlay = map[string]any{}
		if err := json.Unmarshal(overlayJSON, &overlay); err != nil {
			return persistence.BreakpointHitRow{}, fmt.Errorf("unmarshal overlay: %w", err)
		}
	}
	return persistence.BreakpointHitRow{
		Seq:           seq,
		ID:            id,
		BreakpointID:  bpID,
		InstanceID:    instanceID,
		NodeRunID:     nodeRunID,
		FrameID:       frameID,
		Checkpoint:    persistence.BreakpointCheckpoint(checkpoint),
		Mode:          persistence.BreakpointMode(mode),
		Snapshot:      snapshot,
		HitAt:         hitAt,
		ResumedAt:     resumedAt,
		ResumedByKey:  resumedByKey,
		ResumeOverlay: overlay,
	}, nil
}
