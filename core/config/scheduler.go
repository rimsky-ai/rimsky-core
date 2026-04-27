// Package config provides library entry points for rimsky's three long-running
// processes: scheduler, supervisor, control API. Each entry point takes a
// typed config struct and returns a handle with a Shutdown method.
package config

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// SchedulerConfig wires a scheduler process. Defaults apply when fields
// are zero values.
//
// StoreFactories + Stores follow the same shape as SupervisorConfig: the
// deployer registers the factories it has linked in and supplies the parsed
// `stores.yml`; StartScheduler builds the per-process *store.Registry from
// the pair. The scheduler needs the registry for the §13.5 step-4
// visibility-timeout sweep over postgres pick-policy items tables.
type SchedulerConfig struct {
	Storage              storage.StorageBackend
	Queue                queue.DispatchQueue
	Clock                shared.Clock
	Logger               shared.Logger
	TickInterval         time.Duration // default 1500ms
	HeartbeatTimeout     time.Duration // default 15s
	OrphanedClaimTimeout time.Duration // default 5×HeartbeatTimeout
	Pool                 *pgxpool.Pool // for advisory lock; optional but recommended
	// StoreFactories enumerates the store-kind factories registered with
	// this process. Required when Stores is non-empty.
	StoreFactories []store.Factory
	// Stores is the parsed YAML stores config (spec §15.1).
	Stores store.StoresConfig
	// NamedLocks is the operator-side named-lock config (spec §15.2).
	// Validated at startup; templates referencing undeclared names are
	// rejected at deploy.
	NamedLocks store.NamedLocksConfig
}

// SchedulerHandle exposes graceful shutdown for a running scheduler process.
type SchedulerHandle interface {
	Shutdown(ctx context.Context) error
}

// StartScheduler starts a scheduler process and returns a handle for
// graceful shutdown.
func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error) {
	registry, err := buildStoreRegistry(cfg.StoreFactories, cfg.Stores)
	if err != nil {
		return nil, fmt.Errorf("StartScheduler: %w", err)
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		registry.Close()
		return nil, fmt.Errorf("StartScheduler: %w", err)
	}
	var lockHolders *store.LockHoldersClient
	if cfg.Pool != nil {
		lockHolders = store.NewLockHoldersClient(cfg.Pool)
	}
	inner := scheduler.Config{
		Storage:              cfg.Storage,
		Queue:                cfg.Queue,
		Clock:                cfg.Clock,
		Logger:               cfg.Logger,
		TickInterval:         cfg.TickInterval,
		HeartbeatTimeout:     cfg.HeartbeatTimeout,
		OrphanedClaimTimeout: cfg.OrphanedClaimTimeout,
		Pool:                 cfg.Pool,
		LockHolders:          lockHolders,
		StoreRegistry:        registry,
	}
	return schedulerHandleWithRegistry{inner: scheduler.Start(inner), registry: registry}, nil
}

// schedulerHandleWithRegistry wraps the scheduler handle to release the
// store registry's per-store pools (postgres store with `connection:`)
// at shutdown.
type schedulerHandleWithRegistry struct {
	inner    SchedulerHandle
	registry *store.Registry
}

func (h schedulerHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	h.registry.Close()
	return err
}
