// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package scheduler main loop.
//
// This is the graph-layer scheduler — it composes the foundation
// integration sweeps (advisory-lock tick gate, stale-heartbeat sweep,
// dispatch-claim orphan sweep, lock-holder orphan reap, ready sweep)
// with the graph-layer sweeps (pure-cascade transitions, frame-engine
// tick). The cron-fire sweep retired with the 2026-05-15 plan B10 / D7
// / E16; cron firing is owned by the bundled `sensors/sensor-cron/`
// service via the Publisher protocol — Subscribe / Unsubscribe /
// ListSubscriptions plus message envelopes POSTed to the generic
// POST /instances/{instance_id}/messages endpoint with
// sender_kind="publisher".
//
// Per tick:
//  1. AdvisoryLocker-guarded TrySchedulerTick skips ticks when another
//     replica holds the lock. A lock-acquisition error is treated as
//     lock-held — the pass is skipped, never run unlocked (the sweeps
//     are periodic recovery; a one-interval delay is benign, while an
//     unlocked run permits concurrent multi-replica sweeping).
//  2. ProcessPureCascade — transition pure-cascade nodes (no executor) to
//     fresh inline and emit recalculate to dependents.
//  3. runtime.SweepStaleHeartbeats — running nodes whose last_heartbeat
//     is older than the cutoff are forced running→stale, supervisor
//     assignment is cleared, a heartbeat_lost event is appended, and the
//     node is re-enqueued (no retry bump — infra event, not application
//     error).
//  4. runtime.SweepOrphanedNodeRuns — dispatch rows whose
//     `last_heartbeat_at` is older than the cutoff are released
//     claimant-guarded so a fresh supervisor can pick them up.
//  5. runtime.SweepOrphanedClaimHandles — `rimsky_claim_handles` rows whose
//     `expires_at < now()` are deleted claimant-guarded. Per v3 spec
//     §7.5, ClaimProducer.Abandon is NOT called — the store's own TTL/sweep
//     handles its internal state. Cascade FK on
//     `rimsky_claim_holders.claim_handle_id` cleans up held-claim rows.
//  6. runtime.SweepReady — executor-backed stale nodes whose deps
//     are all fresh get enqueued for the next claim cycle.
//  7. frame.RunTick — frame-end detection, queue advancement,
//     stuck-frame warning (advisory only), orphan-frame-dispatch reap.
//  8. Breakpoint sweeps — delete TTL-expired
//     `rimsky_instance_breakpoints` rows, auto-resume stale
//     `rimsky_breakpoint_hits` rows on `auto_resume_after_ttl`
//     breakpoints, and reap orphaned-unresumed hit rows abandoned
//     mid-block (block_dispatch / unknown-policy waits whose
//     supervisor crashed or context-canceled before resume). Per
//     spec
//     `.ok-planner/specs/2026-05-24-instance-debugger-design.md` §7.4.
package scheduler

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// Config bundles everything the scheduler loop needs.
//
// ClaimHandles is required for the orphan-reap sweep. When nil the
// corresponding sweep is skipped — this keeps tests that exercise only
// the dispatch-claim / heartbeat / ready sweeps from being forced into
// store wiring.
//
// In v3 the scheduler does not consult any store-side state — the v2
// visibility-timeout sweep is gone, and the orphan reaper no longer
// fires ClaimProducer.Abandon per spec §7.5. There is no StoreRegistry on
// this Config.
type Config struct {
	// Persist is the unified persistence.Tables handle (rimsky_* tables).
	// Required.
	Persist persistence.Tables
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
	ClaimHandles         persistence.ClaimHandleTable
	// SupervisorID is the scheduler's own supervisor id. Used by the
	// parked-nodes sweep (E3) to claim wakes against — every wake
	// transitions phase parked → pending so any executor-running
	// supervisor can pick the row up; the scheduler itself doesn't need
	// to be one.
	SupervisorID string
	// ParkedSweepInterval governs how often the parked-nodes sweep
	// runs. Zero falls back to TickInterval (every tick).
	ParkedSweepInterval time.Duration
	// StoreRegistry is the per-process producer registry. Required for
	// the park_timeout watchdog to fire Abandon on held claims (blessed
	// invariant 13). When nil the watchdog still removes node-run
	// rows but cannot abandon held claims — used in unit tests.
	StoreRegistry *locks.Registry
	// BlobBackend is the active backend for the orphan-blob sweep
	// (D8 / SweepOrphanedBlobs). When nil the sweep is skipped.
	BlobBackend persistence.BlobBackend
	// BlobOrphans is the rimsky_blob_orphans accessor for the orphan-
	// blob sweep. When nil the sweep is skipped.
	BlobOrphans persistence.BlobOrphanTable
	// OrphanBlobSweepInterval governs how often SweepOrphanedBlobs runs.
	// Zero falls back to 1 hour (per BlobConfig.Retention default).
	OrphanBlobSweepInterval time.Duration
	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook. Threaded into per-tick invalidate emits (schedule fire,
	// parked-resume sweep) so `rimsky_invalidates_total` reflects the
	// scheduler's contribution. Nil → no-op.
	Metrics runtime.MetricsHook
	// MaxParkDuration is the deployment-level per-reason
	// max_park_duration cap map (spec §Parked-state taxonomy /
	// Per-reason `max_park_duration` config). Threaded into
	// SweepParkedNodes so the per-reason cap can fire when the per-row
	// col:rimsky_node_runs.max_park_duration_seconds is NULL. Empty /
	// nil → only per-row caps fire.
	MaxParkDuration map[string]time.Duration
	// Retention carries the trailing-window retention parameters. The
	// claim-handle retention sweep (`runtime.SweepClaimHandleRetention`)
	// runs at every tick when `Retention.ClaimHandlesTrailing > 0`,
	// reaping terminal `rimsky_claim_handles` rows past the cutoff
	// (excluding committed-durable rows — the asset surface). Zero /
	// unset trailing → sweep disabled.
	//
	// @concept: claim-lifetime
	// @concept: claim-handle
	Retention runtime.RetentionConfig
}

// Handle is returned from Start. Shutdown signals the loop to exit after the
// current tick completes.
type Handle struct {
	stop chan struct{}
	done chan struct{}
	// lastOrphanBlobSweep tracks when the orphan-blob sweep last ran so
	// we can throttle it to OrphanBlobSweepInterval (typically 1h)
	// rather than running every tick.
	lastOrphanBlobSweep time.Time
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
// (pure-cascade) calls frame.EnqueueOrCoalesce on cfg.Persist, so a nil
// here is a wiring bug that surfaces as an unhelpful NPE deep in the tick
// loop. Fail loudly at Start() instead.
func Start(cfg Config) *Handle {
	if cfg.Persist == nil {
		panic("scheduler.Start: Config.Persist is required (frame engine and invalidate path dereference it)")
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

	if cfg.OrphanBlobSweepInterval == 0 {
		cfg.OrphanBlobSweepInterval = time.Hour
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
		if err := tick(context.Background(), cfg, h); err != nil {
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

// Tick runs a single sweep under the scheduler-tick exclusion.
// Exported so tests can invoke it synchronously against a real
// Postgres-backed driver. Pass nil for h when running outside the
// runLoop (the Handle is used only to track orphan-blob sweep cadence).
func Tick(ctx context.Context, cfg Config) error {
	return tick(ctx, cfg, nil)
}

// tick runs a single sweep under the scheduler-tick exclusion. The
// Handle pointer is used to track per-sweep cadence state (e.g. the
// orphan-blob sweep's last-run timestamp); pass nil for synchronous
// test invocations.
func tick(ctx context.Context, cfg Config, h *Handle) error {
	log := cfg.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// 1. Scheduler-tick exclusion (skipped when no advisory locker wired).
	if cfg.AdvisoryLocker != nil {
		held, release, err := cfg.AdvisoryLocker.TrySchedulerTick(ctx)
		if err != nil {
			// A lock-attempt error is treated as lock-held: the sweeps
			// are periodic recovery, so skipping one interval is benign,
			// while running unlocked under DB flakiness permits exactly
			// the concurrent multi-replica sweeping the lock exists to
			// prevent (@blessed-invariant 7 — mutual exclusion over
			// availability of a single pass).
			log.Warn("tick: TrySchedulerTick failed; skipping sweep pass",
				"error", err.Error())
			return nil
		}
		if !held {
			log.Debug("tick: another replica holds the lock, skipping")
			return nil
		}
		defer release()
	}

	// 2. Pure-cascade sweep. (The cron-fire sweep retired with the
	//    2026-05-15 plan B10 / D7 / E16; cron firing is owned by the
	//    bundled `sensors/sensor-cron/` service.)
	if _, err := ProcessPureCascade(ctx, PureCascadeArgs{
		Persist: cfg.Persist, Queue: cfg.Queue,
		Clock: cfg.Clock, Logger: log,
	}); err != nil {
		log.Warn("tick: ProcessPureCascade failed", "error", err.Error())
	}

	conductorArgs := runtime.ConductorArgs{
		Persist:              cfg.Persist,
		Queue:                cfg.Queue,
		Clock:                cfg.Clock,
		Logger:               log,
		HeartbeatTimeout:     cfg.HeartbeatTimeout,
		OrphanedClaimTimeout: cfg.OrphanedClaimTimeout,
	}

	// 4. Stale-heartbeat sweep.
	if err := runtime.SweepStaleHeartbeats(ctx, conductorArgs); err != nil {
		return err
	}

	// 5. Dispatch-claim sweep (predicate: last_heartbeat_at < cutoff).
	if err := runtime.SweepOrphanedNodeRuns(ctx, conductorArgs); err != nil {
		return err
	}

	// 6. Lock-holder sweep. Skipped when wiring is incomplete. No
	// store verb fired — store's own TTL handles internal state
	// (per v3 spec §7.5).
	if cfg.ClaimHandles != nil {
		if err := runtime.SweepOrphanedClaimHandles(ctx, runtime.OrphanReaperArgs{
			Persist:      cfg.Persist,
			ClaimHandles: cfg.ClaimHandles,
			Logger:       log,
		}); err != nil {
			return err
		}
	}

	// 6b. Claim-handle retention sweep. Reaps terminal claim_handle rows
	// past the configured trailing window (default disabled —
	// `Retention.ClaimHandlesTrailing == 0` skips the sweep). Wired only
	// when ClaimHandles is present; the sweep is idempotent across
	// invocations and runs under the scheduler-tick advisory lock for
	// cross-replica serialization. Spec
	// .ok-planner/specs/2026-05-17-post-data-platform-cleanup-design.md
	// §Claim-handle state-column refactor / retention.
	if cfg.ClaimHandles != nil && cfg.Retention.ClaimHandlesTrailing > 0 {
		now := time.Now()
		if cfg.Clock != nil {
			now = cfg.Clock.Now()
		}
		if _, err := runtime.SweepClaimHandleRetention(ctx, cfg.ClaimHandles, cfg.Retention, now, log); err != nil {
			log.Warn("tick: SweepClaimHandleRetention failed", "error", err.Error())
		}
	}

	// 6c. rimsky_message_idempotencies retention sweep. Reaps dedup
	// rows past the configured trailing window (default 24h). Dedup
	// tokens are short-lived by design — operators with longer retry
	// windows can raise the cap. Runs under the scheduler-tick
	// advisory lock for cross-replica serialization. Spec
	// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
	// §Message idempotency.
	if cfg.Persist != nil && cfg.Retention.MessageIdempotenciesTrailing > 0 {
		now := time.Now()
		if cfg.Clock != nil {
			now = cfg.Clock.Now()
		}
		if _, err := runtime.SweepMessageIdempotencies(ctx, cfg.Persist.MessageIdempotencies(), cfg.Retention, now, log); err != nil {
			log.Warn("tick: SweepMessageIdempotencies failed", "error", err.Error())
		}
	}

	// 6d. Lineage retention sweep (E10). Reaps rimsky_lineage rows past the
	// configured trailing window whose corresponding run / claim_handle is
	// gone. Default disabled — `Retention.LineageTrailing == 0` skips the
	// sweep. Runs under the scheduler-tick advisory lock for cross-replica
	// serialization; errors are logged at Warn and swallowed (matching the
	// SweepClaimHandleRetention / SweepMessageIdempotencies discipline).
	if cfg.Persist != nil && cfg.Retention.LineageTrailing > 0 {
		now := time.Now()
		if cfg.Clock != nil {
			now = cfg.Clock.Now()
		}
		if _, err := runtime.SweepLineageRetention(ctx, cfg.Persist.Lineage(), cfg.Retention, now, log); err != nil {
			log.Warn("tick: SweepLineageRetention failed", "error", err.Error())
		}
	}

	// 6e. Trace retention sweep (E10). Reaps the whole per-instance
	// execution trace under one policy: terminal frame rows (cascading
	// their node_runs) by the lesser of `Retention.RecentFramesKept` (the
	// count cap) and `Retention.TraceTrailing` (the trailing time window),
	// plus the time-keyed event logs by that same window. Fires when
	// EITHER dimension is enabled — a config with only trace_trailing set
	// (no count cap) must still reap, so we cannot gate on RecentFramesKept
	// alone. `now` feeds the trailing-window cutoff inside the sweep.
	if cfg.Persist != nil && (cfg.Retention.RecentFramesKept > 0 || cfg.Retention.TraceTrailing > 0) {
		now := time.Now()
		if cfg.Clock != nil {
			now = cfg.Clock.Now()
		}
		if _, err := runtime.SweepRunTreeRetention(ctx, cfg.Retention, cfg.Persist, now, log); err != nil {
			log.Warn("tick: SweepRunTreeRetention failed", "error", err.Error())
		}
	}

	// 7. Claim-holder GC is no longer needed:
	// rimsky_claim_holders.claim_handle_id has ON DELETE CASCADE, so when
	// the lock-holder row is deleted (at terminal or by orphan reap), the
	// claim-holder rows are cleaned up automatically.

	// 8. (v2's visibility-timeout sweep is gone — each store-service
	// runs its own internal sweep per v3 spec §7.8 obligation #1.)

	// 9. Ready sweep.
	if err := runtime.SweepReady(ctx, conductorArgs); err != nil {
		return err
	}

	// 9b. Parked-nodes sweep (E3 of plan
	// .ok-planner/plans/2026-05-08-platform-extensions-for-agent-consumers.md).
	// Wakes parked rows whose resume_at has elapsed and forces park_timeout
	// failure on rows that overran max_park_duration_seconds.
	if cfg.SupervisorID != "" {
		if err := runtime.SweepParkedNodes(ctx, runtime.ParkedSweepArgs{
			Persist:          cfg.Persist,
			Queue:            cfg.Queue,
			Clock:            cfg.Clock,
			Logger:           log,
			SupervisorID:     cfg.SupervisorID,
			ClaimHandles:     cfg.ClaimHandles,
			AdvisoryLocker:   cfg.AdvisoryLocker,
			StoreRegistry:    cfg.StoreRegistry,
			Metrics:          cfg.Metrics,
			PerReasonMaxPark: cfg.MaxParkDuration,
		}); err != nil {
			log.Warn("tick: SweepParkedNodes failed", "error", err.Error())
		}
	}

	// 9c. Orphan-blob sweep (D8). Drains rimsky_blob_orphans entries
	// whose reap_after has elapsed; calls BlobBackend.Delete for each
	// and removes the tracker row. Throttled to OrphanBlobSweepInterval
	// (default 1h) so it doesn't run every 1.5s tick. Wired only when
	// both BlobBackend and BlobOrphans are present.
	if cfg.BlobBackend != nil && cfg.BlobOrphans != nil {
		now := cfg.Clock.Now()
		if h == nil || h.lastOrphanBlobSweep.IsZero() || now.Sub(h.lastOrphanBlobSweep) >= cfg.OrphanBlobSweepInterval {
			if err := runtime.SweepOrphanedBlobs(ctx, runtime.OrphanBlobsArgs{
				Persist:     cfg.Persist,
				BlobOrphans: cfg.BlobOrphans,
				Backend:     cfg.BlobBackend,
				Clock:       cfg.Clock,
				Logger:      log,
			}); err != nil {
				log.Warn("tick: SweepOrphanedBlobs failed", "error", err.Error())
			}
			if h != nil {
				h.lastOrphanBlobSweep = now
			}
		}
	}

	// 10. Frame engine tick (frame-end detection, queue advancement,
	// stuck-frame warning (advisory only), orphan-frame-dispatch reap).
	if cfg.Persist != nil && cfg.Queue != nil {
		if err := frame.RunTick(ctx, cfg.Persist, cfg.Queue, log, frameMetricsAdapter(cfg.Metrics)); err != nil {
			log.Warn("tick: frame.RunTick failed", "error", err.Error())
		}
	}

	// 11. Message-delivery sweep — for each running frame, deliver
	// pending messages per the per-instance frame_delivery_mode. Fired
	// after frame.RunTick so newly-promoted running frames pick up
	// their messages on the same tick. Idempotent re-fire is safe: a
	// row that's already delivered_at + frame_id is filtered out by
	// `ListPendingForInstance`. Per spec §Unified message layer.
	if cfg.Persist != nil && cfg.Clock != nil {
		if err := runtime.SweepDeliverMessagesForRunningFrames(ctx, cfg.Persist, cfg.Queue, log, cfg.Clock.Now()); err != nil {
			log.Warn("tick: SweepDeliverMessagesForRunningFrames failed", "error", err.Error())
		}
	}

	// 12. Breakpoint sweeps — delete TTL-expired breakpoints,
	// auto-resume stale hits on auto_resume_after_ttl breakpoints, and
	// reap unresumed hits abandoned mid-block by a supervisor crash /
	// context cancel. Per spec
	// .ok-planner/specs/2026-05-24-instance-debugger-design.md §7.4.
	// Errors are logged at Warn and swallowed (matching the
	// SweepClaimHandleRetention / SweepMessageIdempotencies discipline).
	if cfg.Persist != nil {
		bpNow := time.Now()
		if cfg.Clock != nil {
			bpNow = cfg.Clock.Now()
		}
		// Orphaned-unresumed cutoff: how stale an unresumed hit must
		// get before the reaper deletes it. 5 minutes is generous
		// enough that legitimately-paused dispatches under operator
		// inspection don't get yanked out from under their waiters,
		// while still bounding the table size after supervisor
		// restarts under steady load.
		orphanedHitCutoff := bpNow.Add(-5 * time.Minute)
		if err := cfg.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			deleted, err := cfg.Persist.Breakpoints().SweepExpired(ctx, bpNow, tx)
			if err != nil {
				return err
			}
			if deleted > 0 {
				log.Info("tick: SweepExpired breakpoints", "deleted", deleted)
			}
			resumed, err := cfg.Persist.BreakpointHits().AutoResumeStale(ctx, bpNow, tx)
			if err != nil {
				return err
			}
			if resumed > 0 {
				log.Info("tick: AutoResumeStale breakpoint hits", "resumed", resumed)
			}
			orphaned, err := cfg.Persist.BreakpointHits().SweepOrphanedUnresumed(ctx, orphanedHitCutoff, tx)
			if err != nil {
				return err
			}
			if orphaned > 0 {
				log.Info("tick: SweepOrphanedUnresumed breakpoint hits",
					"reaped", orphaned, "cutoff", orphanedHitCutoff.Format(time.RFC3339))
			}
			return nil
		}); err != nil {
			log.Warn("tick: breakpoint sweeps failed", "error", err.Error())
		}
	}
	return nil
}

// frameMetricsAdapter narrows the runtime.MetricsHook to frame's
// minimum surface. Returns nil when no hook is configured so RunTick
// skips the observation.
func frameMetricsAdapter(m runtime.MetricsHook) frame.MetricsHook {
	if m == nil {
		return nil
	}
	return frameDurationOnly{m}
}

type frameDurationOnly struct {
	hook runtime.MetricsHook
}

func (a frameDurationOnly) ObserveFrameDuration(seconds float64) {
	a.hook.ObserveFrameDuration(seconds)
}
