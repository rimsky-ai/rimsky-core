// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/scheduler"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

type SchedulerConfig struct {
	Driver                  persistence.Database
	Clock                   shared.Clock
	Logger                  shared.Logger
	TickInterval            time.Duration
	MaxQuietPeriodDefault   time.Duration
	Stores                  RemoteStoresConfig
	NamedLocks              locks.NamedLocksConfig
	SupervisorID            string
	Blob                    persistence.BlobBackend
	OrphanBlobSweepInterval time.Duration
	Metrics                 runtime.MetricsHook
	Retention               runtime.RetentionConfig
}

type SchedulerHandle interface {
	Shutdown(ctx context.Context) error
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
	registry, err := dialRemoteStores(context.Background(), cfg.Stores, persistStore, nil)
	if err != nil {
		return nil, fmt.Errorf("StartScheduler: %w", err)
	}
	persistQueue := cfg.Driver.Queue()
	if persistQueue == nil {
		registry.Close()
		return nil, fmt.Errorf("StartScheduler: Driver.Queue() returned nil")
	}
	coordinator := cfg.Driver.AdvisoryLocker()
	if coordinator == nil {
		registry.Close()
		return nil, fmt.Errorf("StartScheduler: Driver.Coordinator() returned nil")
	}
	inner := scheduler.Config{
		Persist:                 persistStore,
		Queue:                   persistQueue,
		AdvisoryLocker:          coordinator,
		Clock:                   cfg.Clock,
		Logger:                  cfg.Logger,
		TickInterval:            cfg.TickInterval,
		MaxQuietPeriodDefault:   cfg.MaxQuietPeriodDefault,
		ClaimHandles:            persistStore.ClaimHandles(),
		SupervisorID:            cfg.SupervisorID,
		StoreRegistry:           registry,
		BlobBackend:             cfg.Blob,
		BlobOrphans:             persistStore.BlobOrphans(),
		OrphanBlobSweepInterval: cfg.OrphanBlobSweepInterval,
		Metrics:                 cfg.Metrics,
		Retention:               cfg.Retention,
	}
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	go runAuthSweepLoop(sweepCtx, persistStore, cfg.Clock, cfg.Logger)
	return schedulerHandleWithRegistry{
		inner:       scheduler.Start(inner),
		registry:    registry,
		sweepCancel: sweepCancel,
	}, nil
}

const authSweepInterval = 1 * time.Minute

func runAuthSweepLoop(ctx context.Context, tables persistence.Tables, clock shared.Clock, log shared.Logger) {
	ticker := time.NewTicker(authSweepInterval)
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
	inner       SchedulerHandle
	registry    *locks.Registry
	sweepCancel context.CancelFunc
}

func (h schedulerHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	if h.sweepCancel != nil {
		h.sweepCancel()
	}
	if h.registry != nil {
		h.registry.Close()
	}
	return err
}
