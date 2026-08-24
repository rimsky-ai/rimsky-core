// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

type Config struct {
	Persist        persistence.Tables
	Queue          persistence.Queue
	AdvisoryLocker persistence.AdvisoryLocker
	Clock          shared.Clock
	Logger         shared.Logger
	TickInterval   time.Duration
	// @decision: three-dispatch-deadlines
	MaxQuietPeriodDefault time.Duration
	MaxRuntimeDefault     time.Duration
	ClaimHandles          persistence.ClaimHandleTable
	SupervisorID          string
	ParkedSweepInterval   time.Duration
	ClaimProducerRegistry *locks.Registry
	// @concept: host-daemon-proxy
	LateBindServiceProxies map[string]string
	// @decision: lifecycle-drain-per-role
	LifecycleKick func()
	Metrics       runtime.MetricsHook
	// @concept: claim-lifetime
	// @concept: claim-handle
	Retention runtime.RetentionConfig
}

type Handle struct {
	stop           chan struct{}
	stopOnce       sync.Once
	done           chan struct{}
	ticksCompleted atomic.Uint64
}

// @decision: polling-audit
func (h *Handle) TicksCompleted() uint64 {
	return h.ticksCompleted.Load()
}

func (h *Handle) Shutdown(ctx context.Context) error {
	h.stopOnce.Do(func() { close(h.stop) })
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const defaultTickInterval = 250 * time.Millisecond

func resolveTickInterval(configured time.Duration) time.Duration {
	if configured == 0 {
		return defaultTickInterval
	}
	return configured
}

func resolveNow(clock shared.Clock) time.Time {
	if clock == nil {
		return time.Now()
	}
	return clock.Now()
}

func Start(cfg Config) *Handle {
	if cfg.Persist == nil {
		panic("scheduler.Start: Config.Persist is required (frame engine and invalidate path dereference it)")
	}
	cfg.TickInterval = resolveTickInterval(cfg.TickInterval)
	if cfg.Logger == nil {
		cfg.Logger = shared.SilentLogger{}
	}
	if cfg.Clock == nil {
		cfg.Clock = shared.SystemClock{}
	}
	h := &Handle{stop: make(chan struct{}), done: make(chan struct{})}
	go runLoop(cfg, h)
	return h
}

func runLoop(cfg Config, h *Handle) {
	defer close(h.done)
	cfg.Logger.Info("SCHEDULER.LOOP.STARTED",
		"tick_ms", cfg.TickInterval.Milliseconds(),
		"max_quiet_period_default_ms", cfg.MaxQuietPeriodDefault.Milliseconds(),
	)
	for {
		select {
		case <-h.stop:
			cfg.Logger.Info("SCHEDULER.LOOP.STOPPED")
			return
		default:
		}
		if err := tick(context.Background(), cfg); err != nil {
			cfg.Logger.Error("SCHEDULER.TICK.FAILED", "error", err.Error())
		}
		h.ticksCompleted.Add(1)
		timer := time.NewTimer(cfg.TickInterval)
		select {
		case <-h.stop:
			timer.Stop()
			cfg.Logger.Info("SCHEDULER.LOOP.STOPPED")
			return
		case <-timer.C:
		}
	}
}

func Tick(ctx context.Context, cfg Config) error {
	return tick(ctx, cfg)
}

func tick(ctx context.Context, cfg Config) error {
	log := cfg.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// @concept: advisory-lock
	if cfg.AdvisoryLocker != nil {
		held, release, err := cfg.AdvisoryLocker.TrySchedulerTick(ctx)
		// @decision: sweep-lock-skip-on-error
		if err != nil {
			log.Warn("SCHEDULER.TICKLOCK.ACQUIREFAILED", "detail", "skipping the sweep pass",
				"error", err.Error())
			return nil
		}
		if !held {
			log.Debug("SCHEDULER.TICKLOCK.HELDELSEWHERE", "detail", "another replica holds the lock")
			return nil
		}
		defer release()
	}

	if _, err := ProcessPureCascade(ctx, PureCascadeArgs{
		Persist: cfg.Persist, Queue: cfg.Queue,
		Clock: cfg.Clock, Logger: log,
	}); err != nil {
		log.Warn("SCHEDULER.PURECASCADEPASS.FAILED", "error", err.Error())
	}

	conductorArgs := runtime.ConductorArgs{
		Persist:               cfg.Persist,
		Queue:                 cfg.Queue,
		Clock:                 cfg.Clock,
		Logger:                log,
		MaxQuietPeriodDefault: cfg.MaxQuietPeriodDefault,
		MaxRuntimeDefault:     cfg.MaxRuntimeDefault,
	}

	if err := runtime.SweepExecutorDeadlines(ctx, conductorArgs); err != nil {
		log.Warn("SCHEDULER.EXECUTORDEADLINESWEEP.FAILED", "error", err.Error())
	}

	if cfg.ClaimHandles != nil {
		if err := runtime.SweepOrphanedClaimHandles(ctx, runtime.OrphanReaperArgs{
			Persist:      cfg.Persist,
			ClaimHandles: cfg.ClaimHandles,
			Logger:       log,
		}); err != nil {
			log.Warn("SCHEDULER.ORPHANEDCLAIMHANDLESWEEP.FAILED", "error", err.Error())
		}
	}

	if cfg.ClaimHandles != nil && cfg.Retention.ClaimHandlesTrailing > 0 {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepClaimHandleRetention(ctx, cfg.ClaimHandles, cfg.Retention, now, log); err != nil {
			log.Warn("SCHEDULER.CLAIMHANDLERETENTIONSWEEP.FAILED", "error", err.Error())
		}
	}

	if cfg.Persist != nil && cfg.Retention.MessageIdempotenciesTrailing > 0 {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepMessageIdempotencies(ctx, cfg.Persist.MessageIdempotencies(), cfg.Retention, now, log); err != nil {
			log.Warn("SCHEDULER.MESSAGEIDEMPOTENCYSWEEP.FAILED", "error", err.Error())
		}
	}

	if cfg.Persist != nil && cfg.Retention.LifecycleOutboxTrailing > 0 {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepLifecycleOutbox(ctx, cfg.Persist, cfg.Retention, now, log); err != nil {
			log.Warn("SCHEDULER.LIFECYCLEOUTBOXSWEEP.FAILED", "error", err.Error())
		}
	}

	if cfg.Persist != nil && cfg.Retention.LineageTrailing > 0 {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepLineageRetention(ctx, cfg.Persist.Lineage(), cfg.Retention, now, log); err != nil {
			log.Warn("SCHEDULER.LINEAGERETENTIONSWEEP.FAILED", "error", err.Error())
		}
	}

	if cfg.Persist != nil && (cfg.Retention.RecentFramesKept > 0 || cfg.Retention.TraceTrailing > 0) {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepRunTreeRetention(ctx, cfg.Retention, cfg.Persist, now, log); err != nil {
			log.Warn("SCHEDULER.RUNTREERETENTIONSWEEP.FAILED", "error", err.Error())
		}
	}

	if cfg.SupervisorID != "" {
		if err := runtime.SweepParkedNodes(ctx, runtime.ParkedSweepArgs{
			Persist:      cfg.Persist,
			Queue:        cfg.Queue,
			Clock:        cfg.Clock,
			Logger:       log,
			SupervisorID: cfg.SupervisorID,
			Metrics:      cfg.Metrics,
		}); err != nil {
			log.Warn("SCHEDULER.PARKEDNODESWEEP.FAILED", "error", err.Error())
		}
	}

	if cfg.Persist != nil && cfg.Clock != nil {
		if err := runtime.SweepDeliverTriggeringMessagesForRunningFrames(ctx, cfg.Persist, log, cfg.Clock.Now()); err != nil {
			log.Warn("SCHEDULER.TRIGGERINGMESSAGESWEEP.FAILED", "error", err.Error())
		}
	}

	if cfg.Persist != nil && cfg.Queue != nil {
		// @decision: lifecycle-fanout-after-commit
		delivery := frame.LifecycleDelivery{LateBindServiceProxies: cfg.LateBindServiceProxies, Kick: cfg.LifecycleKick}
		if err := frame.RunTick(ctx, cfg.Persist, cfg.Queue, log, delivery, frameMetricsAdapter(cfg.Metrics)); err != nil {
			log.Warn("SCHEDULER.FRAMETICK.FAILED", "error", err.Error())
		}
	}

	if cfg.Persist != nil {
		bpNow := resolveNow(cfg.Clock)
		orphanedHitCutoff := bpNow.Add(-5 * time.Minute)
		if err := cfg.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			deleted, err := cfg.Persist.Breakpoints().SweepExpired(ctx, bpNow, tx)
			if err != nil {
				return err
			}
			if deleted > 0 {
				log.Info("SCHEDULER.EXPIREDBREAKPOINTSWEEP.COMPLETED", "deleted", deleted)
			}
			resumed, err := cfg.Persist.BreakpointHits().AutoResumeStale(ctx, bpNow, tx)
			if err != nil {
				return err
			}
			if resumed > 0 {
				log.Info("SCHEDULER.STALEBREAKPOINTRESUME.COMPLETED", "resumed", resumed)
			}
			orphaned, err := cfg.Persist.BreakpointHits().SweepOrphanedUnresumed(ctx, orphanedHitCutoff, tx)
			if err != nil {
				return err
			}
			if orphaned > 0 {
				log.Info("SCHEDULER.ORPHANEDBREAKPOINTHITSWEEP.COMPLETED",
					"reaped", orphaned, "cutoff", orphanedHitCutoff.Format(time.RFC3339))
			}
			return nil
		}); err != nil {
			log.Warn("SCHEDULER.BREAKPOINTSWEEP.FAILED", "error", err.Error())
		}
	}
	return nil
}

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
