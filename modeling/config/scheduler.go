// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/scheduler"
	"github.com/fallguy/rimsky/modeling/shared"
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
	// Driver is the unified persistence driver. Required.
	Driver               persistence.Driver
	Clock                shared.Clock
	Logger               shared.Logger
	TickInterval         time.Duration // default 1500ms
	HeartbeatTimeout     time.Duration // default 15s
	OrphanedClaimTimeout time.Duration // default 5×HeartbeatTimeout
	// Stores is the parsed `stores:` block from rimsky.yml. Each entry
	// is dialed at startup and validated against the operator-declared
	// capabilities; mismatches fail StartScheduler.
	Stores RemoteStoresConfig
	// NamedLocks is the operator-side named-lock config. Validated at
	// startup; templates referencing undeclared names are rejected at
	// deploy.
	NamedLocks locks.NamedLocksConfig
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
	if cfg.Driver == nil {
		return nil, fmt.Errorf("StartScheduler: Driver is required")
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		return nil, fmt.Errorf("StartScheduler: %w", err)
	}
	registry, err := dialRemoteStores(context.Background(), cfg.Stores)
	if err != nil {
		return nil, fmt.Errorf("StartScheduler: %w", err)
	}
	persistStore := cfg.Driver.Store()
	if persistStore == nil {
		registry.Close()
		return nil, fmt.Errorf("StartScheduler: Driver.Store() returned nil — driver did not initialize the Store accessor")
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
		Persist:              persistStore,
		Queue:                persistQueue,
		AdvisoryLocker:       coordinator,
		Clock:                cfg.Clock,
		Logger:               cfg.Logger,
		TickInterval:         cfg.TickInterval,
		HeartbeatTimeout:     cfg.HeartbeatTimeout,
		OrphanedClaimTimeout: cfg.OrphanedClaimTimeout,
		LockHolders:          persistStore.LockHolders(),
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
	registry *locks.Registry
}

func (h schedulerHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	if h.registry != nil {
		h.registry.Close()
	}
	return err
}
