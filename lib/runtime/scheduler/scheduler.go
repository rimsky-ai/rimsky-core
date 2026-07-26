// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
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
	MaxQuietPeriodDefault   time.Duration
	MaxRuntimeDefault       time.Duration
	ClaimHandles            persistence.ClaimHandleTable
	SupervisorID            string
	ParkedSweepInterval     time.Duration
	ClaimProducerRegistry   *locks.Registry
	LifecycleSubs           *lifecycle.Registry
	LifecyclePeersForSpec   func(tplSpec node.TemplateSpec) []string
	BlobBackend             persistence.BlobBackend
	BlobOrphans             persistence.BlobOrphanTable
	OrphanBlobSweepInterval time.Duration
	Metrics                 runtime.MetricsHook
	// @concept: claim-lifetime
	// @concept: claim-handle
	Retention runtime.RetentionConfig
}

type Handle struct {
	stop                chan struct{}
	stopOnce            sync.Once
	done                chan struct{}
	lastOrphanBlobSweep time.Time
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
		"max_quiet_period_default_ms", cfg.MaxQuietPeriodDefault.Milliseconds(),
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

func Tick(ctx context.Context, cfg Config) error {
	return tick(ctx, cfg, nil)
}

func tick(ctx context.Context, cfg Config, h *Handle) error {
	log := cfg.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// @concept: advisory-lock
	if cfg.AdvisoryLocker != nil {
		held, release, err := cfg.AdvisoryLocker.TrySchedulerTick(ctx)
		if err != nil {
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

	if _, err := ProcessPureCascade(ctx, PureCascadeArgs{
		Persist: cfg.Persist, Queue: cfg.Queue,
		Clock: cfg.Clock, Logger: log,
	}); err != nil {
		log.Warn("tick: ProcessPureCascade failed", "error", err.Error())
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
		log.Warn("tick: SweepExecutorDeadlines failed", "error", err.Error())
	}

	if cfg.ClaimHandles != nil {
		if err := runtime.SweepOrphanedClaimHandles(ctx, runtime.OrphanReaperArgs{
			Persist:      cfg.Persist,
			ClaimHandles: cfg.ClaimHandles,
			Logger:       log,
		}); err != nil {
			log.Warn("tick: SweepOrphanedClaimHandles failed", "error", err.Error())
		}
	}

	if cfg.ClaimHandles != nil && cfg.Retention.ClaimHandlesTrailing > 0 {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepClaimHandleRetention(ctx, cfg.ClaimHandles, cfg.Retention, now, log); err != nil {
			log.Warn("tick: SweepClaimHandleRetention failed", "error", err.Error())
		}
	}

	if cfg.Persist != nil && cfg.Retention.MessageIdempotenciesTrailing > 0 {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepMessageIdempotencies(ctx, cfg.Persist.MessageIdempotencies(), cfg.Retention, now, log); err != nil {
			log.Warn("tick: SweepMessageIdempotencies failed", "error", err.Error())
		}
	}

	if cfg.Persist != nil && cfg.Retention.LineageTrailing > 0 {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepLineageRetention(ctx, cfg.Persist.Lineage(), cfg.Retention, now, log); err != nil {
			log.Warn("tick: SweepLineageRetention failed", "error", err.Error())
		}
	}

	if cfg.Persist != nil && (cfg.Retention.RecentFramesKept > 0 || cfg.Retention.TraceTrailing > 0) {
		now := resolveNow(cfg.Clock)
		if _, err := runtime.SweepRunTreeRetention(ctx, cfg.Retention, cfg.Persist, now, log); err != nil {
			log.Warn("tick: SweepRunTreeRetention failed", "error", err.Error())
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
			log.Warn("tick: SweepParkedNodes failed", "error", err.Error())
		}
	}

	if cfg.BlobBackend != nil && cfg.BlobOrphans != nil {
		now := resolveNow(cfg.Clock)
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

	if cfg.Persist != nil && cfg.Clock != nil {
		if err := runtime.SweepDeliverTriggeringMessagesForRunningFrames(ctx, cfg.Persist, log, cfg.Clock.Now()); err != nil {
			log.Warn("tick: SweepDeliverTriggeringMessagesForRunningFrames failed", "error", err.Error())
		}
	}

	if cfg.Persist != nil && cfg.Queue != nil {
		scopeFanout := runtime.FrameRunScopeTerminalFanout(cfg.Persist, cfg.AdvisoryLocker, cfg.LifecycleSubs, cfg.LifecyclePeersForSpec)
		if err := frame.RunTick(ctx, cfg.Persist, cfg.Queue, log, scopeFanout, frameMetricsAdapter(cfg.Metrics)); err != nil {
			log.Warn("tick: frame.RunTick failed", "error", err.Error())
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
