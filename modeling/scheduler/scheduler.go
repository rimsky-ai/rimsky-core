// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package scheduler main loop.
//
// This is the modeling-side scheduler — it composes the foundation
// integration sweeps (advisory-lock tick gate, stale-heartbeat sweep,
// dispatch-claim orphan sweep, lock-holder orphan reap, ready sweep)
// with the modeling-side sweeps (cron schedules, pure-cascade
// transitions, frame-engine tick).
//
// Per tick:
//  1. AdvisoryLocker-guarded TrySchedulerTick skips ticks when another
//     replica holds the lock. Best-effort — if lock acquisition errors we
//     fall through to an unlocked tick rather than dropping work.
//  2. ProcessSchedules — fire due cron schedules, emit invalidate per target.
//  3. ProcessPureCascade — transition pure-cascade nodes (no executor) to
//     fresh inline and emit recalculate to dependents.
//  4. integration.SweepStaleHeartbeats — running nodes whose last_heartbeat
//     is older than the cutoff are forced running→stale, supervisor
//     assignment is cleared, a heartbeat_lost event is appended, and the
//     node is re-enqueued (no retry bump — infra event, not application
//     error).
//  5. integration.SweepOrphanedClaims — dispatch rows whose
//     `last_heartbeat_at` is older than the cutoff are released
//     claimant-guarded so a fresh supervisor can pick them up.
//  6. integration.SweepLockHolders — `rimsky_claim_handle` rows whose
//     `expires_at < now()` are deleted claimant-guarded. Per v3 spec
//     §7.5, Store.Abandon is NOT called — the store's own TTL/sweep
//     handles its internal state. Cascade FK on
//     `rimsky_claim_holders.claim_handle_id` cleans up held-claim rows.
//  7. integration.SweepReady — executor-backed stale nodes whose deps
//     are all fresh get enqueued for the next claim cycle.
//  8. frame.RunTick — frame-end detection, queue advancement,
//     stuck-frame warning (advisory only), orphan-frame-dispatch reap.
package scheduler

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/frame"
	"github.com/fallguy/rimsky/modeling/shared"
)

// Config bundles everything the scheduler loop needs.
//
// LockHolders is required for the orphan-reap sweep. When nil the
// corresponding sweep is skipped — this keeps tests that exercise only
// the dispatch-claim / heartbeat / ready sweeps from being forced into
// store wiring.
//
// In v3 the scheduler does not consult any store-side state — the v2
// visibility-timeout sweep is gone, and the orphan reaper no longer
// fires Store.Abandon per spec §7.5. There is no StoreRegistry on
// this Config.
type Config struct {
	// Persist is the unified persistence.Store handle (rimsky_* tables).
	// Required.
	Persist persistence.Store
	// Queue is the dispatch-queue accessor. Required.
	Queue persistence.Queue
	// AdvisoryLocker carries the cross-process synchronization primitives
	// (scheduler-tick exclusion, etc.). Required for the per-tick guard.
	AdvisoryLocker       persistence.AdvisoryLocker
	Clock                shared.Clock
	Logger               shared.Logger
	TickInterval         time.Duration
	HeartbeatTimeout     time.Duration
	OrphanedClaimTimeout time.Duration // default: 5 × HeartbeatTimeout
	LockHolders          persistence.LockHoldersStore
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
//
// Panics if cfg.Persist is nil — every code path that emits an invalidate
// (cron fire, pure-cascade) calls frame.EnqueueOrCoalesce on cfg.Persist,
// so a nil here is a wiring bug that surfaces as an unhelpful NPE deep in
// the tick loop. Fail loudly at Start() instead.
func Start(cfg Config) *Handle {
	if cfg.Persist == nil {
		panic("scheduler.Start: Config.Persist is required (frame engine, invalidate path, schedule firing all dereference it)")
	}
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

// tick runs a single sweep under the scheduler-tick exclusion. Exported
// so tests can invoke it synchronously against a real Postgres-backed
// driver.
func tick(ctx context.Context, cfg Config) error {
	log := cfg.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// 1. Scheduler-tick exclusion (skipped when no advisory locker wired).
	if cfg.AdvisoryLocker != nil {
		held, release, err := cfg.AdvisoryLocker.TrySchedulerTick(ctx)
		if err != nil {
			log.Warn("tick: TrySchedulerTick failed; running unlocked",
				"error", err.Error())
		} else if !held {
			log.Debug("tick: another replica holds the lock, skipping")
			return nil
		} else {
			defer release()
		}
	}

	// 2. ProcessSchedules (cron fire → invalidate).
	if _, err := ProcessSchedules(ctx,
		cfg.Persist,
		scheduleDispatcherAdapter{
			Persist: cfg.Persist, Queue: cfg.Queue,
			Clock: cfg.Clock, Logger: log,
		},
		cfg.Clock, log,
	); err != nil {
		log.Warn("tick: ProcessSchedules failed", "error", err.Error())
	}

	// 3. Pure-cascade sweep.
	if _, err := ProcessPureCascade(ctx, PureCascadeArgs{
		Persist: cfg.Persist, Queue: cfg.Queue,
		Clock: cfg.Clock, Logger: log,
	}); err != nil {
		log.Warn("tick: ProcessPureCascade failed", "error", err.Error())
	}

	conductorArgs := integration.ConductorArgs{
		Persist:              cfg.Persist,
		Queue:                cfg.Queue,
		Clock:                cfg.Clock,
		Logger:               log,
		HeartbeatTimeout:     cfg.HeartbeatTimeout,
		OrphanedClaimTimeout: cfg.OrphanedClaimTimeout,
	}

	// 4. Stale-heartbeat sweep.
	if err := integration.SweepStaleHeartbeats(ctx, conductorArgs); err != nil {
		return err
	}

	// 5. Dispatch-claim sweep (predicate: last_heartbeat_at < cutoff).
	if err := integration.SweepOrphanedClaims(ctx, conductorArgs); err != nil {
		return err
	}

	// 6. Lock-holder sweep. Skipped when wiring is incomplete. No
	// store verb fired — store's own TTL handles internal state
	// (per v3 spec §7.5).
	if cfg.LockHolders != nil {
		if err := integration.SweepLockHolders(ctx, integration.OrphanReaperArgs{
			Persist:     cfg.Persist,
			LockHolders: cfg.LockHolders,
			Logger:      log,
		}); err != nil {
			return err
		}
	}

	// 7. Claim-holder GC is no longer needed:
	// rimsky_claim_holders.claim_handle_id has ON DELETE CASCADE, so when
	// the lock-holder row is deleted (at terminal or by orphan reap), the
	// claim-holder rows are cleaned up automatically.

	// 8. (v2's visibility-timeout sweep is gone — each store-service
	// runs its own internal sweep per v3 spec §7.8 obligation #1.)

	// 9. Ready sweep.
	if err := integration.SweepReady(ctx, conductorArgs); err != nil {
		return err
	}

	// 10. Frame engine tick (frame-end detection, queue advancement,
	// stuck-frame warning (advisory only), orphan-frame-dispatch reap).
	if cfg.Persist != nil && cfg.Queue != nil {
		if err := frame.RunTick(ctx, cfg.Persist, cfg.Queue, log); err != nil {
			log.Warn("tick: frame.RunTick failed", "error", err.Error())
		}
	}
	return nil
}

// --- Adapter bridging InvalidateNode to MessageDispatcher. --------------

// scheduleDispatcherAdapter implements MessageDispatcher by calling
// InvalidateNode with the scheduler's persistence + queue + clock.
type scheduleDispatcherAdapter struct {
	Persist persistence.Store
	Queue   persistence.Queue
	Clock   shared.Clock
	Logger  shared.Logger
}

func (a scheduleDispatcherAdapter) EmitInvalidate(ctx context.Context, req InvalidateRequest) error {
	return integration.InvalidateNode(ctx, integration.InvalidateArgs{
		Persist:      a.Persist,
		Queue:        a.Queue,
		Clock:        a.Clock,
		Logger:       a.Logger,
		SourceNodeID: req.SourceNodeID,
		TargetNodeID: req.TargetNodeID,
		Reason:       req.Reason,
	})
}
