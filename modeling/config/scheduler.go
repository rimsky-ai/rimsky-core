// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/integration"
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
	// SupervisorID identifies this scheduler instance for the parked-
	// nodes sweep (E3 of the 2026-05-08 platform-extensions plan). The
	// sweep transitions parked rows back to phase='pending' so any
	// executor-running supervisor can pick them up; the scheduler
	// passes its own id only for audit logging and idempotency.
	SupervisorID string
	// Blob is the active BlobBackend; threaded into the orphan-blob
	// sweep so it can call Backend.Delete on reaped handles. Nil → the
	// orphan-blob sweep is skipped (no spilled bytes to reap).
	Blob persistence.BlobBackend
	// OrphanBlobSweepInterval governs how often the orphan-blob sweep
	// runs. Threaded through to scheduler.Config from rimsky.yml's
	// persistence.blob.retention.orphan_sweep_interval. Zero → defaults
	// to 1h inside scheduler.Start.
	OrphanBlobSweepInterval time.Duration
	// Metrics is the prometheus instrumentation hook (plan I2).
	// Threaded into scheduler.Config.Metrics so per-tick invalidate
	// emits (cron schedule fire, parked-resume sweep) and frame.RunTick
	// observations land on the shared registry. Optional; nil → no-op
	// everywhere. Production wiring constructs an
	// observability.RegistryHook from the per-process MetricsRegistry.
	Metrics integration.MetricsHook
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
		ClaimHandles:         persistStore.ClaimHandles(),
		SupervisorID:         cfg.SupervisorID,
		// StoreRegistry is the dialed producer registry; required by the
		// park_timeout watchdog to fire Abandon on held claims (blessed
		// invariant 13).
		StoreRegistry: registry,
		// BlobBackend / BlobOrphans drive the orphan-blob sweep (D8).
		// cfg.Blob is nil when no backend was installed at startup
		// (typical of unit tests); persistStore.BlobOrphans() returns
		// the rimsky_blob_orphans accessor on the unified store.
		BlobBackend:             cfg.Blob,
		BlobOrphans:             persistStore.BlobOrphans(),
		OrphanBlobSweepInterval: cfg.OrphanBlobSweepInterval,
		Metrics:                 cfg.Metrics,
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
