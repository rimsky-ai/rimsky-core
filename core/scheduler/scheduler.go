// Package scheduler main loop.
//
// Per tick:
//  1. Advisory-lock guard (pg_try_advisory_lock) skips ticks when another
//     replica holds the lock. Best-effort — if lock acquisition errors we
//     fall through to an unlocked tick rather than dropping work.
//  2. ProcessSchedules — fire due cron schedules, emit invalidate per target.
//  3. ProcessPureCascade — transition pure-cascade nodes (no executor) to
//     fresh inline and emit recalculate to dependents.
//  4. Stale-heartbeat sweep — running nodes whose last_heartbeat is older
//     than the cutoff are forced running→stale, supervisor assignment is
//     cleared, a heartbeat_lost event is appended, and the node is
//     re-enqueued (no retry bump — infra event, not application error).
//  5. Dispatch-claim sweep — dispatch rows whose `last_heartbeat_at` is
//     older than the cutoff are released claimant-guarded so a fresh
//     supervisor can pick them up.
//  6. Orphan-reap — `rimsky_lock_holders` rows whose `expires_at < now()`
//     are deleted claimant-guarded. Per v3 spec §7.5, Store.Abandon is NOT
//     called — the store's own TTL/sweep handles its internal state.
//     Cascade FK on `rimsky_claim_holders.lock_holder_id` cleans up
//     held-claim rows.
//  7. (Claim-holder GC removed — the cascade FK on
//     `rimsky_claim_holders.lock_holder_id` makes the dedicated
//     leaked-claim-holder reap unnecessary.)
//  8. (v2's visibility-timeout sweep over operator-owned items tables is
//     gone — each store-service runs its own internal sweep per v3 spec
//     §7.8 obligation #1.)
//  9. Ready sweep — executor-backed stale nodes whose deps are all fresh
//     get enqueued for the next claim cycle.
package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// RimskySchedulerTickLockKey is a fixed 64-bit advisory-lock key used to
// serialize scheduler ticks across replicas. Each tick attempts
// pg_try_advisory_lock(key); if another replica holds it we skip this tick
// and try again next interval.
//
// @blessed-invariant "advisory lock prevents double-emit of
// heartbeat_lost / orphaned_claim_released / lock_orphan_reaped across
// replicas"
//
// Without this guard, two live schedulers during a deployment overlap would
// both execute the stale-heartbeat / dispatch-claim / lock-holder sweeps on
// the same rows, producing duplicate events (and, pre-claimant-guard, a
// risk of releasing a fresh supervisor's live claim or lock holder). Any
// refactor that removes the lock guard must preserve equivalent
// replica-exclusive sweep semantics.
const RimskySchedulerTickLockKey int64 = 4853127298010834892

// Config bundles everything the scheduler loop needs.
//
// LockHolders and Pool are required for the orphan-reap sweep. When
// either is nil the corresponding sweep is skipped — this keeps tests
// that exercise only the dispatch-claim / heartbeat / ready sweeps
// from being forced into store wiring.
//
// In v3 the scheduler does not consult any store-side state — the v2
// visibility-timeout sweep is gone, and the orphan reaper no longer
// fires Store.Abandon per spec §7.5. There is no StoreRegistry on
// this Config.
type Config struct {
	Storage              storage.StorageBackend
	Queue                queue.DispatchQueue
	Clock                shared.Clock
	Logger               shared.Logger
	TickInterval         time.Duration
	HeartbeatTimeout     time.Duration
	OrphanedClaimTimeout time.Duration // default: 5 × HeartbeatTimeout
	Pool                 *pgxpool.Pool // required for advisory-lock guard + orphan reap
	LockHolders          *store.LockHoldersClient
}

// Handle is returned from Start. Shutdown signals the loop to exit after the
// current tick completes.
type Handle struct {
	stop chan struct{}
	done chan struct{}
}

// Shutdown signals the loop to stop. Returns when the loop has exited, or
// when ctx is canceled.
func (h *Handle) Shutdown(ctx context.Context) error {
	select {
	case <-h.stop:
		// already closed; still wait for done
	default:
		close(h.stop)
	}
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Start launches the scheduler tick loop. Errors inside a tick are logged;
// the loop never returns on its own unless Handle.Shutdown is invoked.
func Start(cfg Config) *Handle {
	if cfg.TickInterval == 0 {
		cfg.TickInterval = 1500 * time.Millisecond
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = 15 * time.Second
	}
	// @blessed-invariant "orphan-claim cutoff default = 5 × heartbeat_timeout"
	//
	// A tighter cutoff (e.g. 2 × heartbeat_timeout) can sweep a live-but-slow
	// supervisor's claim — when the supervisor has issued Claim() but hasn't
	// yet flipped the node's state to running (slow dep-data fetch, cold
	// start, etc.). The fresh supervisor would then run the handler
	// concurrently with the original. The runners defensively re-read the
	// claim before entering the handler as a hard backstop, but preserving
	// the 5× floor keeps the window small enough in practice.
	if cfg.OrphanedClaimTimeout == 0 {
		cfg.OrphanedClaimTimeout = 5 * cfg.HeartbeatTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = shared.SilentLogger{}
	}

	h := &Handle{stop: make(chan struct{}), done: make(chan struct{})}
	go runLoop(cfg, h)
	return h
}

func runLoop(cfg Config, h *Handle) {
	defer close(h.done)
	cfg.Logger.Info("scheduler started",
		"tick_ms", cfg.TickInterval.Milliseconds(),
		"heartbeat_timeout_ms", cfg.HeartbeatTimeout.Milliseconds(),
		"orphaned_claim_timeout_ms", cfg.OrphanedClaimTimeout.Milliseconds(),
	)
	for {
		select {
		case <-h.stop:
			cfg.Logger.Info("scheduler stopped")
			return
		default:
		}
		if err := tick(context.Background(), cfg); err != nil {
			cfg.Logger.Error("scheduler tick failed", "error", err.Error())
		}
		// Sleep with early-wake on stop.
		timer := time.NewTimer(cfg.TickInterval)
		select {
		case <-h.stop:
			timer.Stop()
			cfg.Logger.Info("scheduler stopped")
			return
		case <-timer.C:
		}
	}
}

// tick runs a single sweep under the advisory-lock guard. Exported so tests
// can invoke it synchronously against a real Postgres pool.
func tick(ctx context.Context, cfg Config) error {
	log := cfg.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// 1. Advisory-lock guard (skipped when no pool provided).
	if cfg.Pool != nil {
		conn, err := cfg.Pool.Acquire(ctx)
		if err != nil {
			log.Warn("tick: advisory lock acquire failed; running unlocked",
				"error", err.Error())
		} else {
			var locked bool
			row := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", RimskySchedulerTickLockKey)
			if scanErr := row.Scan(&locked); scanErr != nil {
				log.Warn("tick: advisory lock query failed; running unlocked",
					"error", scanErr.Error())
				conn.Release()
			} else if !locked {
				// Another replica holds the tick lock. Skip this tick.
				log.Debug("tick: another replica holds the lock, skipping")
				conn.Release()
				return nil
			} else {
				// Lock acquired — release on tick exit.
				defer func() {
					// Best-effort unlock; connection release below drops any
					// held session locks anyway.
					_, _ = conn.Exec(context.Background(),
						"SELECT pg_advisory_unlock($1)",
						RimskySchedulerTickLockKey)
					conn.Release()
				}()
			}
		}
	}

	// 2. ProcessSchedules (cron fire → invalidate).
	if _, err := ProcessSchedules(ctx,
		cfg.Storage,
		scheduleDispatcherAdapter{
			Storage: cfg.Storage, Queue: cfg.Queue,
			Clock: cfg.Clock, Logger: log,
		},
		cfg.Clock, log,
	); err != nil {
		log.Warn("tick: ProcessSchedules failed", "error", err.Error())
	}

	// 3. Pure-cascade sweep.
	if _, err := ProcessPureCascade(ctx, PureCascadeArgs{
		Storage: cfg.Storage, Queue: cfg.Queue,
		Clock: cfg.Clock, Logger: log,
	}); err != nil {
		log.Warn("tick: ProcessPureCascade failed", "error", err.Error())
	}

	// 4. Stale-heartbeat sweep.
	if err := sweepStaleHeartbeats(ctx, cfg, log); err != nil {
		return err
	}

	// 5. Dispatch-claim sweep (predicate: last_heartbeat_at < cutoff).
	if err := sweepOrphanedClaims(ctx, cfg, log); err != nil {
		return err
	}

	// 6. Lock-holder sweep. Skipped when wiring is incomplete. No
	// substrate verb fired — store's own TTL handles internal state
	// (per v3 spec §7.5).
	if cfg.LockHolders != nil && cfg.Pool != nil {
		if err := sweepLockHolders(ctx, cfg, log); err != nil {
			return err
		}
	}

	// 7. Claim-holder GC is no longer needed:
	// rimsky_claim_holders.lock_holder_id has ON DELETE CASCADE, so when
	// the lock-holder row is deleted (at terminal or by orphan reap), the
	// claim-holder rows are cleaned up automatically.

	// 8. (v2's visibility-timeout sweep is gone — each store-service
	// runs its own internal sweep per v3 spec §7.8 obligation #1.)

	// 9. Ready sweep.
	if err := sweepReady(ctx, cfg, log); err != nil {
		return err
	}

	// 10. Frame engine tick (frame-end detection, queue advancement,
	// stuck-frame reaper, orphan-frame-dispatch reap).
	// Skipped when the pool isn't wired (test harnesses without a pool).
	if cfg.Pool != nil {
		if err := frame.RunTick(ctx, cfg.Pool, log); err != nil {
			log.Warn("tick: frame.RunTick failed", "error", err.Error())
		}
	}
	return nil
}

func sweepStaleHeartbeats(ctx context.Context, cfg Config, log shared.Logger) error {
	cutoff := cfg.Clock.Now().Add(-cfg.HeartbeatTimeout)
	stale, err := cfg.Storage.Nodes().ListWithStaleHeartbeat(ctx, cutoff, nil)
	if err != nil {
		return err
	}
	for _, n := range stale {
		nodeID := n.ID
		instanceID := n.InstanceID
		payload := map[string]any{
			"supervisor_id":     n.AssignedSupervisorID,
			"last_heartbeat_at": n.LastHeartbeatAt,
		}
		if err := cfg.Storage.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &nodeID, InstanceID: &instanceID,
			Kind:    "heartbeat_lost",
			Payload: payload,
		}, nil); err != nil {
			log.Warn("tick: append heartbeat_lost failed",
				"node_id", n.ID.String(), "error", err.Error())
		}
		// running → stale (also clears assigned_supervisor_id + heartbeat
		// as part of the state transition; no separate clear call needed).
		if err := cfg.Storage.Nodes().UpdateState(ctx, n.ID,
			shared.NodeStateStale, node.ReasonHeartbeatLost, nil); err != nil {
			log.Warn("tick: heartbeat_lost state transition failed",
				"node_id", n.ID.String(), "error", err.Error())
			continue
		}
		// Re-enqueue without bumping retry_counter. RequiredStores is left
		// empty here for the same reason recalculate.go leaves it empty: the
		// scheduler tick does not have the in-memory template registry
		// threaded through, and an empty RequiredStores trivially satisfies
		// the supervisor-pool predicate (RequiredStores ⊆ AcceptedStores).
		// Threading the registry through is a separate task.
		// FrameID is sourced from the node row — heartbeat-lost nodes were
		// running in a frame and that frame_id remains the running frame
		// (per blessed-invariant 19).
		if n.FrameID == nil {
			log.Warn("tick: skip re-enqueue: node frame_id is nil",
				"node_id", n.ID.String())
			continue
		}
		if err := cfg.Queue.Enqueue(ctx, queue.DispatchRequest{
			NodeID:         n.ID,
			ExecutorName:   n.Executor,
			RequiredStores: []string{},
			EnqueuedAt:     cfg.Clock.Now(),
			FrameID:        *n.FrameID,
		}); err != nil {
			log.Warn("tick: re-enqueue after heartbeat_lost failed",
				"node_id", n.ID.String(), "error", err.Error())
		}
	}
	return nil
}

func sweepOrphanedClaims(ctx context.Context, cfg Config, log shared.Logger) error {
	// §7.4 orphan-claim sweep: predicate column is
	// `rimsky_dispatch.last_heartbeat_at`. The Queue's ListOrphanedClaims
	// already encodes that predicate; the cutoff we pass is
	// `now() - 5 × heartbeat_timeout`. (Distinct from the §7.5 lock-holder
	// orphan reaper in sweep_locks.go which keys on
	// `rimsky_lock_holders.expires_at`.)
	cutoff := cfg.Clock.Now().Add(-cfg.OrphanedClaimTimeout)
	orphans, err := cfg.Queue.ListOrphanedClaims(ctx, cutoff)
	if err != nil {
		return err
	}
	for _, o := range orphans {
		nodeID := o.NodeID
		prior := ""
		if o.ClaimedBy != nil {
			prior = *o.ClaimedBy
		}
		// Claimant-guarded release: passing prior ensures a fresh supervisor
		// that re-claimed the row between ListOrphanedClaims and this UPDATE
		// keeps its live claim intact.
		if err := cfg.Queue.ReleaseClaim(ctx, o.ID, prior); err != nil {
			log.Warn("tick: release orphaned claim failed",
				"dispatch_id", o.ID.String(), "error", err.Error())
			continue
		}
		var hb time.Time
		if o.LastHeartbeatAt != nil {
			hb = *o.LastHeartbeatAt
		}
		if err := cfg.Storage.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &nodeID,
			Kind:   "orphaned_claim_released",
			Payload: map[string]any{
				"dispatch_id":       o.ID.String(),
				"prior_claimed_by":  prior,
				"last_heartbeat_at": hb,
			},
		}, nil); err != nil {
			log.Warn("tick: append orphaned_claim_released failed",
				"dispatch_id", o.ID.String(), "error", err.Error())
		}
	}
	return nil
}

func sweepReady(ctx context.Context, cfg Config, log shared.Logger) error {
	ready, err := cfg.Storage.Nodes().ListReadyForDispatch(ctx, nil)
	if err != nil {
		return err
	}
	for _, n := range ready {
		// RequiredStores is left empty; see sweepStaleHeartbeats for the
		// rationale (mirrors recalculate.go's `[]string{}` placeholder).
		// FrameID is sourced from the node row — every stale node is part
		// of the in-flight frame (blessed-invariant 19). A nil frame_id
		// here means the frame engine has not yet advanced this node's
		// queued frame; we skip and re-evaluate next tick.
		if n.FrameID == nil {
			log.Debug("tick: ready-sweep skip: node frame_id is nil",
				"node_id", n.ID.String())
			continue
		}
		if err := cfg.Queue.Enqueue(ctx, queue.DispatchRequest{
			NodeID:         n.ID,
			ExecutorName:   n.Executor,
			RequiredStores: []string{},
			EnqueuedAt:     cfg.Clock.Now(),
			FrameID:        *n.FrameID,
		}); err != nil {
			log.Warn("tick: ready-sweep enqueue failed",
				"node_id", n.ID.String(), "error", err.Error())
		}
	}
	return nil
}

// --- Adapter bridging InvalidateNode to MessageDispatcher. --------------

// scheduleDispatcherAdapter implements MessageDispatcher by calling
// InvalidateNode with the scheduler's storage + queue + clock.
type scheduleDispatcherAdapter struct {
	Storage storage.StorageBackend
	Queue   queue.DispatchQueue
	Clock   shared.Clock
	Logger  shared.Logger
}

func (a scheduleDispatcherAdapter) EmitInvalidate(ctx context.Context, req InvalidateRequest) error {
	return InvalidateNode(ctx, InvalidateArgs{
		Storage:      a.Storage,
		Queue:        a.Queue,
		Clock:        a.Clock,
		Logger:       a.Logger,
		SourceNodeID: req.SourceNodeID,
		TargetNodeID: req.TargetNodeID,
		Reason:       req.Reason,
	})
}
