package frame

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Logger is the minimum logging surface RunTick needs. Both
// *log/slog.Logger and shared.Logger (the scheduler's structured-log
// wrapper) satisfy this; keeping it tiny lets core/frame avoid
// importing core/shared without losing the scheduler's pre-bound
// fields when the scheduler wires its logger through.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// RunTick performs one frame-engine iteration. The caller must hold the
// scheduler-tick advisory lock (blessed-invariant 7).
//
// Steps per §4.1 of the spec:
//  1. Detect frame-end (transition running → completed|failed).
//  2. Advance queued (serial_queue and coalesce) — promote oldest queued to running.
//  3. Reap stuck frames (timeout exceeded with no claimed dispatches).
//  4. Reap orphan dispatches (frame in terminal state but dispatch still claimed).
//
// Each step opens its own short tx so partial failures don't poison the
// whole tick. The advisory lock guarantees serialization across replicas;
// within one process this is just a sequential loop.
func RunTick(ctx context.Context, db PgxBeginner, logger Logger) error {
	if err := runFrameEndDetection(ctx, db, logger); err != nil {
		return fmt.Errorf("frame.RunTick: frame-end: %w", err)
	}
	if err := runAdvanceQueued(ctx, db, logger); err != nil {
		return fmt.Errorf("frame.RunTick: advance: %w", err)
	}
	if err := runReapStuckFrames(ctx, db, logger); err != nil {
		return fmt.Errorf("frame.RunTick: reap stuck: %w", err)
	}
	if err := runReapOrphanFrameDispatches(ctx, db, logger); err != nil {
		return fmt.Errorf("frame.RunTick: reap orphan: %w", err)
	}
	return nil
}

// PgxBeginner is the minimum surface RunTick needs from a pgxpool.Pool.
// Defined locally so callers don't have to import pgxpool just to satisfy
// the engine signature, and so tests can pass any tx-creator.
type PgxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func runFrameEndDetection(ctx context.Context, db PgxBeginner, logger Logger) error {
	type pending struct {
		frameID    uuid.UUID
		instanceID uuid.UUID
	}

	// Step 1: collect pendings outside any subsequent transition tx so a
	// single bad frame doesn't poison the whole tick.
	listTx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	// The NOT EXISTS predicate filters by `n.frame_id = f.frame_id` so a
	// stale/running node from a different (concurrent or later) frame
	// cannot block this frame's end. Under v1 there is at most one frame
	// per instance in 'running' state (uq_rimsky_frames_running) and any
	// in-flight node carries that frame's frame_id; the per-frame filter
	// is robust under future Rule 3b parallel-buffered semantics (spec §10.6).
	rows, err := listTx.Query(ctx, `
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
		_ = listTx.Rollback(ctx)
		return err
	}
	var pendings []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.frameID, &p.instanceID); err != nil {
			rows.Close()
			_ = listTx.Rollback(ctx)
			return err
		}
		pendings = append(pendings, p)
	}
	rows.Close()
	if err := listTx.Commit(ctx); err != nil {
		return err
	}

	// Step 2: per-frame transition tx so a single frame's failure leaves
	// the rest unaffected.
	for _, p := range pendings {
		if err := transitionFrameEnd(ctx, db, p.frameID, p.instanceID, logger); err != nil {
			logger.Warn("frame.end.transition_failed",
				"frame_id", p.frameID,
				"instance_id", p.instanceID,
				"error", err.Error())
			continue
		}
	}
	return nil
}

// transitionFrameEnd applies one frame's running → completed|failed
// transition in its own short tx. Determines the outcome by looking at
// the frame's nodes (any failed → failed, else completed).
//
// After the frame transitions to terminal, the same tx evaluates the
// instance's terminal predicate (no remaining queued/running frames,
// no stale/running nodes) and sets rimsky_instances.terminated_at if
// satisfied. Per docs/specs/2026-05-01-control-plane-and-store-
// lifecycle-design.md §2.4: idempotent, set-once.
func transitionFrameEnd(ctx context.Context, db PgxBeginner, frameID, instanceID uuid.UUID, logger Logger) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var anyFailed bool
	if err := tx.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM rimsky_nodes n
            WHERE n.instance_id = $1
              AND n.frame_id = $2
              AND n.state = 'failed'
        )
    `, instanceID, frameID).Scan(&anyFailed); err != nil {
		return err
	}
	finalState := StateCompleted
	if anyFailed {
		finalState = StateFailed
	}
	cmd, err := tx.Exec(ctx, `
        UPDATE rimsky_frames
        SET state = $1, ended_at = now()
        WHERE frame_id = $2 AND state = 'running'
    `, finalState, frameID)
	if err != nil {
		return err
	}
	// Mark the instance terminated when no further work remains. The
	// UPDATE's WHERE clause encodes the terminal predicate so the call
	// is set-once and idempotent: once terminated_at is non-NULL or any
	// frame/node says otherwise, the row count is zero and the call is
	// a no-op. Per spec §2.4.
	if _, err := tx.Exec(ctx, `
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
    `, instanceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if cmd.RowsAffected() == 1 {
		logger.Info("frame.end",
			"frame_id", frameID,
			"instance_id", instanceID,
			"final_state", finalState)
	}
	return nil
}

func runAdvanceQueued(ctx context.Context, db PgxBeginner, logger Logger) error {
	type advance struct {
		frameID    uuid.UUID
		instanceID uuid.UUID
		sources    []uuid.UUID
	}

	// Step 1: collect candidates in a short read tx so the per-frame
	// advancement below is independent.
	listTx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	rows, err := listTx.Query(ctx, `
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
		_ = listTx.Rollback(ctx)
		return err
	}
	var advances []advance
	for rows.Next() {
		var a advance
		if err := rows.Scan(&a.frameID, &a.instanceID, &a.sources); err != nil {
			rows.Close()
			_ = listTx.Rollback(ctx)
			return err
		}
		advances = append(advances, a)
	}
	rows.Close()
	if err := listTx.Commit(ctx); err != nil {
		return err
	}

	// Step 2: per-frame advance tx so a single bad frame (e.g. wedged
	// source node) does not roll back siblings on other instances.
	for _, a := range advances {
		if err := advanceOneFrame(ctx, db, a.frameID, a.instanceID, a.sources, logger); err != nil {
			logger.Warn("frame.start.advance_failed",
				"frame_id", a.frameID,
				"instance_id", a.instanceID,
				"error", err.Error())
			continue
		}
	}
	return nil
}

// advanceOneFrame promotes one queued frame to running and writes its
// source nodes stale-with-frame_id. Runs in its own tx so failures are
// isolated to this frame; a wedged source node logs a warning and
// returns nil so the queued frame remains and the next tick can retry
// (rather than spamming errors forever — see review Issue 6).
func advanceOneFrame(ctx context.Context, db PgxBeginner, frameID, instanceID uuid.UUID, sources []uuid.UUID, logger Logger) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	cmd, err := tx.Exec(ctx, `
        UPDATE rimsky_frames
        SET state = 'running', started_at = now()
        WHERE frame_id = $1 AND state = 'queued'
    `, frameID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		// Another replica won; nothing to do.
		return nil
	}

	// Set source nodes stale with frame_id.
	// In-bounds states: fresh, failed (legitimate transitions per §4.3
	// step 2), OR stale-with-nil-frame_id (the initial-run case for
	// freshly created nodes that started life stale and have no
	// previous frame). Stale-with-frame_id is rejected — that's the
	// "frame-end mis-detected" failure mode the spec calls out.
	for _, src := range sources {
		cmd, err := tx.Exec(ctx, `
            UPDATE rimsky_nodes
            SET state = 'stale', frame_id = $1, updated_at = now()
            WHERE instance_id = $2 AND id = $3
              AND (state IN ('fresh','failed')
                   OR (state = 'stale' AND frame_id IS NULL))
        `, frameID, instanceID, src)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() != 1 {
			// Source node not in expected bounds. Roll back the frame's
			// promotion so the queued row remains; warn but do not error.
			// Spamming an error forever (the previous behaviour) wedges
			// the engine; the next tick will retry, and operator tools
			// can intervene if a node is genuinely stuck.
			logger.Warn("frame.start.source_not_in_bounds",
				"frame_id", frameID,
				"instance_id", instanceID,
				"source_node_id", src)
			return nil
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	logger.Info("frame.start",
		"frame_id", frameID,
		"instance_id", instanceID,
		"source_node_ids", sources)
	return nil
}

func runReapStuckFrames(ctx context.Context, db PgxBeginner, logger Logger) error {
	type stuck struct {
		frameID    uuid.UUID
		instanceID uuid.UUID
		timeout    int64
	}

	listTx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	rows, err := listTx.Query(ctx, `
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
		_ = listTx.Rollback(ctx)
		return err
	}
	var stuckFrames []stuck
	for rows.Next() {
		var s stuck
		if err := rows.Scan(&s.frameID, &s.instanceID, &s.timeout); err != nil {
			rows.Close()
			_ = listTx.Rollback(ctx)
			return err
		}
		stuckFrames = append(stuckFrames, s)
	}
	rows.Close()
	if err := listTx.Commit(ctx); err != nil {
		return err
	}

	for _, s := range stuckFrames {
		if err := reapOneStuckFrame(ctx, db, s.frameID, s.instanceID); err != nil {
			logger.Warn("frame.stuck.reap_failed",
				"frame_id", s.frameID,
				"instance_id", s.instanceID,
				"timeout_ms", s.timeout,
				"error", err.Error())
			continue
		}
		logger.Warn("frame.stuck.reaped",
			"frame_id", s.frameID,
			"instance_id", s.instanceID,
			"timeout_ms", s.timeout)
	}
	return nil
}

// reapOneStuckFrame fails one stuck frame in its own tx. Marks all
// stale/running nodes in the instance as failed, transitions the frame
// to failed, and (per spec §2.4) sets rimsky_instances.terminated_at
// when no further work remains. Without the terminated_at write here
// the next frame-end-detection pass would not pick the instance up
// (its SELECT filters by state='running'), and the instance row would
// stay un-terminated forever — leaking the OnInstanceTerminated event.
func reapOneStuckFrame(ctx context.Context, db PgxBeginner, frameID, instanceID uuid.UUID) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
        UPDATE rimsky_nodes
        SET state = 'failed', updated_at = now()
        WHERE instance_id = $1 AND state IN ('stale','running')
    `, instanceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
        UPDATE rimsky_frames SET state = 'failed', ended_at = now()
        WHERE frame_id = $1 AND state = 'running'
    `, frameID); err != nil {
		return err
	}
	// Mirrors the terminal-predicate from transitionFrameEnd. Set-once,
	// idempotent: the predicate ensures the row count is zero unless
	// terminated_at is currently NULL and no queued/running frame and
	// no stale/running node remains for this instance.
	if _, err := tx.Exec(ctx, `
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
    `, instanceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// runReapOrphanFrameDispatches releases dispatch claims whose frame has
// already reached a terminal state. Per blessed-invariant 4 (claimant-
// guarded release), the per-row UPDATE filters by `claimed_by =
// supervisor_id` so a fresh supervisor that re-claimed the row between
// the SELECT and the UPDATE keeps its live claim. The SELECT runs in
// its own short tx; each row's release runs in its own tx so a single
// failed row does not roll back siblings.
func runReapOrphanFrameDispatches(ctx context.Context, db PgxBeginner, logger Logger) error {
	type orphan struct {
		dispatchID uuid.UUID
		claimedBy  string
		frameID    uuid.UUID
	}

	listTx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	rows, err := listTx.Query(ctx, `
        SELECT d.id, d.claimed_by, d.frame_id
        FROM rimsky_dispatch d
        JOIN rimsky_frames f ON f.frame_id = d.frame_id
        WHERE d.claimed_by IS NOT NULL
          AND f.state IN ('completed','failed')
    `)
	if err != nil {
		_ = listTx.Rollback(ctx)
		return err
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.dispatchID, &o.claimedBy, &o.frameID); err != nil {
			rows.Close()
			_ = listTx.Rollback(ctx)
			return err
		}
		orphans = append(orphans, o)
	}
	rows.Close()
	if err := listTx.Commit(ctx); err != nil {
		return err
	}

	for _, o := range orphans {
		if err := releaseOrphanDispatch(ctx, db, o.dispatchID, o.claimedBy); err != nil {
			logger.Warn("frame.orphan_dispatch.release_failed",
				"dispatch_id", o.dispatchID,
				"frame_id", o.frameID,
				"prior_claimed_by", o.claimedBy,
				"error", err.Error())
			continue
		}
		logger.Warn("frame.orphan_dispatch.reaped",
			"dispatch_id", o.dispatchID,
			"frame_id", o.frameID,
			"prior_claimed_by", o.claimedBy)
	}
	return nil
}

// releaseOrphanDispatch nulls the claim fields on one rimsky_dispatch
// row using a per-row tx, claimant-guarded by `claimed_by = $2` so a
// fresh supervisor's claim cannot be silently released.
func releaseOrphanDispatch(ctx context.Context, db PgxBeginner, dispatchID uuid.UUID, priorClaimedBy string) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
        UPDATE rimsky_dispatch
        SET claimed_by = NULL, claimed_at = NULL, last_heartbeat_at = NULL
        WHERE id = $1 AND claimed_by = $2
    `, dispatchID, priorClaimedBy); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
