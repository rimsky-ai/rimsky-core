// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/scheduler"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

type SchedulerConfig struct {
	Driver                 persistence.Database
	Clock                  shared.Clock
	Logger                 shared.Logger
	TickInterval           time.Duration
	MaxQuietPeriodDefault  time.Duration
	MaxRuntimeDefault      time.Duration
	ClaimProducers         RemoteClaimProducersConfig
	Executors              ExecutorsConfig
	Publishers             RemotePublishersConfig
	NamedLocks             locks.NamedLocksConfig
	SupervisorID           string
	AuthSweepInterval      time.Duration
	Metrics                runtime.MetricsHook
	Retention              runtime.RetentionConfig
	LateBindServiceProxies map[string]string
	ServiceAuth            string

	// @decision: lifecycle-drain-per-role
	SharedLifecycleDrain *runtime.LifecycleReconciler

	// @decision: lifecycle-subscriber-at-least-once-delivery
	ServiceDeliveryStallAfter time.Duration
}

type SchedulerHandle interface {
	Shutdown(ctx context.Context) error

	// @decision: polling-audit
	TicksCompleted() uint64
}

func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error) {
	if cfg.Driver == nil {
		return nil, fmt.Errorf("StartScheduler: Driver is required")
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		return nil, fmt.Errorf("StartScheduler: %w", err)
	}
	persistStore := cfg.Driver.Tables()
	if persistStore == nil {
		return nil, fmt.Errorf("StartScheduler: Database.Tables() returned nil — driver did not initialize the Tables accessor")
	}
	stopIdentity := func() {}
	if cfg.ServiceAuth == service.ServiceAuthMTLS {
		_, _, cancel, err := installServiceIdentity(context.Background(), persistStore, cfg.SupervisorID, cfg.Clock, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("StartScheduler: %w", err)
		}
		stopIdentity = cancel
	}
	// @concept: service-address-book
	registry := newAddressBookProducerRegistry(persistStore, nil)
	persistQueue := cfg.Driver.Queue()
	if persistQueue == nil {
		stopIdentity()
		registry.Close()
		return nil, fmt.Errorf("StartScheduler: Driver.Queue() returned nil")
	}
	// @concept: advisory-lock
	advisoryLocker := cfg.Driver.AdvisoryLocker()
	if advisoryLocker == nil {
		stopIdentity()
		registry.Close()
		return nil, fmt.Errorf("StartScheduler: Driver.AdvisoryLocker() returned nil")
	}
	// @decision: lifecycle-drain-per-role
	lifecycleDrain := cfg.SharedLifecycleDrain
	var lifecycleSubs *lifecycle.Registry
	if lifecycleDrain == nil {
		subs, err := DialLifecycleSubscribers(context.Background(), cfg.ClaimProducers, cfg.Executors, cfg.Publishers)
		if err != nil {
			stopIdentity()
			registry.Close()
			return nil, fmt.Errorf("StartScheduler: dial lifecycle subscribers: %w", err)
		}
		lifecycleSubs = subs
		lifecycleDrain = runtime.NewLifecycleReconciler(runtime.LifecycleReconcilerConfig{
			Persist:        persistStore,
			AdvisoryLocker: advisoryLocker,
			Subscribers:    lifecycleSubs,
			Clock:          cfg.Clock,
			Logger:         cfg.Logger,
			StallAfter:     cfg.ServiceDeliveryStallAfter,
		})
	}
	inner := scheduler.Config{
		Persist:                persistStore,
		Queue:                  persistQueue,
		AdvisoryLocker:         advisoryLocker,
		Clock:                  cfg.Clock,
		Logger:                 cfg.Logger,
		TickInterval:           cfg.TickInterval,
		MaxQuietPeriodDefault:  cfg.MaxQuietPeriodDefault,
		MaxRuntimeDefault:      cfg.MaxRuntimeDefault,
		ClaimHandles:           persistStore.ClaimHandles(),
		SupervisorID:           cfg.SupervisorID,
		ClaimProducerRegistry:  registry,
		LateBindServiceProxies: cfg.LateBindServiceProxies,
		LifecycleKick:          lifecycleDrain.Kick,
		Metrics:                cfg.Metrics,
		Retention:              cfg.Retention,
	}
	authSweepEvery := cfg.AuthSweepInterval
	if authSweepEvery == 0 {
		authSweepEvery = authSweepInterval
	}
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		runAuthSweepLoop(sweepCtx, persistStore, cfg.Clock, cfg.Logger, authSweepEvery)
	}()
	handle := schedulerHandleWithRegistry{
		inner:         scheduler.Start(inner),
		registry:      registry,
		lifecycleSubs: lifecycleSubs,
		sweepCancel:   sweepCancel,
		sweepDone:     sweepDone,
		stopIdentity:  stopIdentity,
	}
	// @decision: lifecycle-drain-per-role
	if cfg.SharedLifecycleDrain == nil {
		drainCtx, drainCancel := context.WithCancel(context.Background())
		go lifecycleDrain.Run(drainCtx)
		handle.lifecycleDrain = lifecycleDrain
		handle.drainCancel = drainCancel
	}
	return handle, nil
}

const authSweepInterval = 1 * time.Minute

func runAuthSweepLoop(ctx context.Context, tables persistence.Tables, clock shared.Clock, log shared.Logger, interval time.Duration) {
	if log == nil {
		log = shared.SilentLogger{}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := runtime.SweepRotationGrace(ctx, tables, clock, log)
			if err != nil {
				log.Error("AUTH.SWEEP.FAILED", "err", err.Error())
			} else if n > 0 {
				log.Info("AUTH.SWEEP.COMPLETED", "swept", n)
			}
			// @concept: api-key
			expired, err := runtime.SweepKeyExpiry(ctx, tables, clock, log)
			if err != nil {
				log.Error("AUTH.EXPIRYSWEEP.FAILED", "err", err.Error())
			} else if expired > 0 {
				log.Info("AUTH.EXPIRYSWEEP.COMPLETED", "expired", expired)
			}
		}
	}
}

type schedulerHandleWithRegistry struct {
	inner          SchedulerHandle
	registry       *locks.Registry
	lifecycleSubs  *lifecycle.Registry
	sweepCancel    context.CancelFunc
	sweepDone      <-chan struct{}
	stopIdentity   func()
	lifecycleDrain *runtime.LifecycleReconciler
	drainCancel    context.CancelFunc
}

// @decision: polling-audit
func (h schedulerHandleWithRegistry) TicksCompleted() uint64 {
	return h.inner.TicksCompleted()
}

func (h schedulerHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	if h.lifecycleDrain != nil {
		h.lifecycleDrain.Stop()
	}
	if h.drainCancel != nil {
		h.drainCancel()
	}
	if h.sweepCancel != nil {
		h.sweepCancel()
	}
	if h.sweepDone != nil {
		select {
		case <-h.sweepDone:
		case <-ctx.Done():
		}
	}
	if h.stopIdentity != nil {
		h.stopIdentity()
	}
	if h.registry != nil {
		h.registry.Close()
	}
	if h.lifecycleSubs != nil {
		h.lifecycleSubs.Close()
	}
	return err
}
