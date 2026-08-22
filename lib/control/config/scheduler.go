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
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/scheduler"
)

type SchedulerConfig struct {
	Driver                  persistence.Database
	Clock                   shared.Clock
	Logger                  shared.Logger
	TickInterval            time.Duration
	MaxQuietPeriodDefault   time.Duration
	MaxRuntimeDefault       time.Duration
	ClaimProducers          RemoteClaimProducersConfig
	Executors               ExecutorsConfig
	Publishers              RemotePublishersConfig
	NamedLocks              locks.NamedLocksConfig
	SupervisorID            string
	Blob                    persistence.BlobBackend
	OrphanBlobSweepInterval time.Duration
	AuthSweepInterval       time.Duration
	Metrics                 runtime.MetricsHook
	Retention               runtime.RetentionConfig
	LifecyclePeersForSpec   func(tplSpec node.TemplateSpec) []string
	PeerAuth                string
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
	if cfg.PeerAuth == peer.PeerAuthMTLS {
		_, _, cancel, err := installPeerIdentity(context.Background(), persistStore, cfg.SupervisorID, cfg.Clock, cfg.Logger)
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
	lifecycleSubs, err := DialLifecycleSubscribers(context.Background(), cfg.ClaimProducers, cfg.Executors, cfg.Publishers)
	if err != nil {
		stopIdentity()
		registry.Close()
		return nil, fmt.Errorf("StartScheduler: dial lifecycle subscribers: %w", err)
	}
	inner := scheduler.Config{
		Persist:                 persistStore,
		Queue:                   persistQueue,
		AdvisoryLocker:          advisoryLocker,
		Clock:                   cfg.Clock,
		Logger:                  cfg.Logger,
		TickInterval:            cfg.TickInterval,
		MaxQuietPeriodDefault:   cfg.MaxQuietPeriodDefault,
		MaxRuntimeDefault:       cfg.MaxRuntimeDefault,
		ClaimHandles:            persistStore.ClaimHandles(),
		SupervisorID:            cfg.SupervisorID,
		ClaimProducerRegistry:   registry,
		LifecycleSubs:           lifecycleSubs,
		LifecyclePeersForSpec:   cfg.LifecyclePeersForSpec,
		BlobBackend:             cfg.Blob,
		BlobOrphans:             persistStore.BlobOrphans(),
		OrphanBlobSweepInterval: cfg.OrphanBlobSweepInterval,
		Metrics:                 cfg.Metrics,
		Retention:               cfg.Retention,
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
	return schedulerHandleWithRegistry{
		inner:         scheduler.Start(inner),
		registry:      registry,
		lifecycleSubs: lifecycleSubs,
		sweepCancel:   sweepCancel,
		sweepDone:     sweepDone,
		stopIdentity:  stopIdentity,
	}, nil
}

const authSweepInterval = 1 * time.Minute

func runAuthSweepLoop(ctx context.Context, tables persistence.Tables, clock shared.Clock, log shared.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := runtime.SweepRotationGrace(ctx, tables, clock, log)
			if err != nil && log != nil {
				log.Error("auth.sweep.failed", "err", err.Error())
				continue
			}
			if n > 0 && log != nil {
				log.Info("auth.sweep.done", "swept", n)
			}
		}
	}
}

type schedulerHandleWithRegistry struct {
	inner         SchedulerHandle
	registry      *locks.Registry
	lifecycleSubs *lifecycle.Registry
	sweepCancel   context.CancelFunc
	sweepDone     <-chan struct{}
	stopIdentity  func()
}

// @decision: polling-audit
func (h schedulerHandleWithRegistry) TicksCompleted() uint64 {
	return h.inner.TicksCompleted()
}

func (h schedulerHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
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
