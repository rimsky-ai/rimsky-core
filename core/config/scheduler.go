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
// All three rimsky processes (scheduler, supervisor, control-api) dial
// the configured stores at startup and run the Capabilities() handshake
// per the 2026-05-01 control-plane spec §3.5 / §6.6 — even though the
// scheduler does not call any of the runtime verbs. Failing fast when
// a configured store is unreachable or its capabilities mismatch keeps
// rimsky's three processes in lock-step on the operator-declared
// topology.
type SchedulerConfig struct {
	Storage              storage.StorageBackend
	Queue                queue.DispatchQueue
	Clock                shared.Clock
	Logger               shared.Logger
	TickInterval         time.Duration // default 1500ms
	HeartbeatTimeout     time.Duration // default 15s
	OrphanedClaimTimeout time.Duration // default 5×HeartbeatTimeout
	Pool                 *pgxpool.Pool // for advisory lock + orphan reap
	// Stores is the parsed `stores:` block from rimsky.yml. Each entry
	// is dialed at startup and validated against the operator-declared
	// capabilities; mismatches fail StartScheduler.
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
// Validates `cfg.NamedLocks` and dials each store in `cfg.Stores` at
// startup (per spec §3.5 / §6.6). On any failure the dialed clients
// are closed and the error is returned for the caller to propagate as
// a startup failure. The scheduler does not call any of the four
// runtime verbs at present, but the dialed registry is held for the
// process lifetime so the handshake guard remains active.
func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error) {
	if err := cfg.NamedLocks.Validate(); err != nil {
		return nil, fmt.Errorf("StartScheduler: %w", err)
	}
	registry, err := dialRemoteStores(context.Background(), cfg.Stores)
	if err != nil {
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
	return schedulerHandleWithRegistry{
		inner:    scheduler.Start(inner),
		registry: registry,
	}, nil
}

// schedulerHandleWithRegistry wraps the scheduler handle plus the
// dialed store registry; Shutdown closes both.
type schedulerHandleWithRegistry struct {
	inner    SchedulerHandle
	registry *store.Registry
}

func (h schedulerHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	if h.registry != nil {
		h.registry.Close()
	}
	return err
}
