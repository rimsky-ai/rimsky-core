// Public API for embedding a rimsky supervisor into a host process.
package config

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/supervisor"
)

// SupervisorConfig wires a supervisor process. Per spec §6.1 — the
// stores config is a thin "name → endpoint + declared capabilities"
// form; the supervisor dials each entry and validates the
// Capabilities() handshake at startup.
type SupervisorConfig struct {
	SupervisorID      string
	Storage           storage.StorageBackend
	Queue             queue.DispatchQueue
	Clock             shared.Clock
	Logger            shared.Logger
	Concurrency       int
	HeartbeatInterval time.Duration
	ClaimPollInterval time.Duration
	Resolver          executor.Resolver
	// Stores is the parsed `stores:` block from stores.yml: an
	// endpoint URL plus declared capabilities per entry.
	Stores RemoteStoresConfig
	// NamedLocks is the operator-side named-lock config. Empty /
	// missing → no named locks declared; templates that reference
	// named locks will fail registry-dependent validation.
	NamedLocks   store.NamedLocksConfig
	CallbackHost string
	CallbackPort int
	// CallbackAdvertiseHost / CallbackAdvertisePort override the
	// host:port embedded in the `callback_url` handed to executors.
	CallbackAdvertiseHost string
	CallbackAdvertisePort int
}

// SupervisorHandle is the lifecycle handle returned by StartSupervisor.
type SupervisorHandle interface {
	Shutdown(ctx context.Context) error
	CallbackAddr() string
}

// StartSupervisor starts a supervisor process. SupervisorID must be
// unique across running supervisors (typically hostname+pid).
func StartSupervisor(cfg SupervisorConfig) (SupervisorHandle, error) {
	if cfg.SupervisorID == "" {
		return nil, fmt.Errorf("StartSupervisor: SupervisorID required")
	}
	if cfg.Resolver == nil {
		return nil, fmt.Errorf("StartSupervisor: Resolver required")
	}
	registry, err := dialRemoteStores(context.Background(), cfg.Stores)
	if err != nil {
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		registry.Close()
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	inner, err := supervisor.Start(supervisor.Config{
		SupervisorID:          cfg.SupervisorID,
		Storage:               cfg.Storage,
		Queue:                 cfg.Queue,
		Clock:                 cfg.Clock,
		Logger:                cfg.Logger,
		Concurrency:           cfg.Concurrency,
		HeartbeatInterval:     cfg.HeartbeatInterval,
		ClaimPollInterval:     cfg.ClaimPollInterval,
		Resolver:              cfg.Resolver,
		StoreRegistry:         registry,
		NamedLocks:            cfg.NamedLocks,
		CallbackHost:          cfg.CallbackHost,
		CallbackPort:          cfg.CallbackPort,
		CallbackAdvertiseHost: cfg.CallbackAdvertiseHost,
		CallbackAdvertisePort: cfg.CallbackAdvertisePort,
	})
	if err != nil {
		registry.Close()
		return nil, err
	}
	return supervisorHandleWithRegistry{inner: inner, registry: registry}, nil
}

// supervisorHandleWithRegistry wraps supervisor.Handle to release the
// remote-store gRPC connections at shutdown.
type supervisorHandleWithRegistry struct {
	inner    SupervisorHandle
	registry *store.Registry
}

func (h supervisorHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	h.registry.Close()
	return err
}

func (h supervisorHandleWithRegistry) CallbackAddr() string {
	return h.inner.CallbackAddr()
}
