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

// SchedulerConfig wires a scheduler process. Defaults apply when
// fields are zero values. Per spec §6.1.
//
// In v3 the scheduler no longer dials remote stores at startup — the
// v2 visibility-timeout sweep is gone and the orphan reaper does not
// fire Store.Abandon per spec §7.5. The Stores field is retained on
// the config struct for forwards/backwards compatibility with
// embedders that pass a single config bundle to all three Start*
// functions, but it is not consulted here.
type SchedulerConfig struct {
	Storage              storage.StorageBackend
	Queue                queue.DispatchQueue
	Clock                shared.Clock
	Logger               shared.Logger
	TickInterval         time.Duration // default 1500ms
	HeartbeatTimeout     time.Duration // default 15s
	OrphanedClaimTimeout time.Duration // default 5×HeartbeatTimeout
	Pool                 *pgxpool.Pool // for advisory lock + orphan reap
	// Stores is the parsed `stores:` block from stores.yml. Unused by
	// the scheduler in v3 (kept for embedder ergonomics).
	Stores RemoteStoresConfig
	// NamedLocks is the operator-side named-lock config. Validated at
	// startup; templates referencing undeclared names are rejected at
	// deploy.
	NamedLocks store.NamedLocksConfig
}

// SchedulerHandle exposes graceful shutdown for a running scheduler
// process.
type SchedulerHandle interface {
	Shutdown(ctx context.Context) error
}

// StartScheduler starts a scheduler process and returns a handle for
// graceful shutdown.
//
// Validates `cfg.NamedLocks` at startup; templates referencing
// undeclared names are rejected at deploy time by the control-api.
// The scheduler does not dial remote stores — see the SchedulerConfig
// docstring.
func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error) {
	_ = context.Background() // ctx kept for future use by sweep wiring
	if err := cfg.NamedLocks.Validate(); err != nil {
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
	}
	return schedulerHandleNoRegistry{inner: scheduler.Start(inner)}, nil
}

// schedulerHandleNoRegistry wraps the scheduler handle. v3 drops the
// remote-store registry — the scheduler no longer consults stores.
type schedulerHandleNoRegistry struct {
	inner SchedulerHandle
}

func (h schedulerHandleNoRegistry) Shutdown(ctx context.Context) error {
	return h.inner.Shutdown(ctx)
}
