// Plan A Task 10.6 — thin entry-point wrapper over supervisor.Start.
//
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

// SupervisorConfig wires a supervisor process per spec §10.2.
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
	// StoreFactories enumerates the store-kind factories registered with
	// this process. The deployer's main() builds this list from the set of
	// store implementations it has linked in (filesystem, postgres, stub,
	// custom). Required when Stores is non-empty.
	StoreFactories []store.Factory
	// Stores is the parsed YAML stores config (spec §14.1). Each entry is
	// keyed by operator-chosen store name; the value's "kind" picks a
	// factory from StoreFactories. The supervisor's `accepted_stores`
	// (§14.2) is derived from the resulting registry's store-name set.
	Stores store.StoresConfig
	// NamedLocks is the operator-side named-lock config (spec §15.2).
	// Keys are operator-chosen lock names; values carry the limit.
	// Empty / missing → no named locks declared; templates that
	// reference named locks will fail registry-dependent validation.
	NamedLocks   store.NamedLocksConfig
	CallbackHost string
	CallbackPort int
	// CallbackAdvertiseHost / CallbackAdvertisePort override the host:port
	// embedded in the `callback_url` handed to executors. When empty/zero
	// the supervisor advertises the listener addr — which is wrong in
	// containerized deployments where the listener binds `0.0.0.0`. Set
	// these to a peer-reachable hostname:port (e.g. `rimsky-supervisor:9100`
	// in docker-compose, or the Service DNS in Kubernetes).
	CallbackAdvertiseHost string
	CallbackAdvertisePort int
}

// SupervisorHandle is the lifecycle handle returned by StartSupervisor.
type SupervisorHandle interface {
	Shutdown(ctx context.Context) error
	CallbackAddr() string
}

// StartSupervisor starts a supervisor process. SupervisorID must be unique
// across running supervisors (typically hostname+pid).
func StartSupervisor(cfg SupervisorConfig) (SupervisorHandle, error) {
	if cfg.SupervisorID == "" {
		return nil, fmt.Errorf("StartSupervisor: SupervisorID required")
	}
	if cfg.Resolver == nil {
		return nil, fmt.Errorf("StartSupervisor: Resolver required")
	}
	registry, err := buildStoreRegistry(cfg.StoreFactories, cfg.Stores)
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
// store registry's per-store pools (postgres store with `connection:`)
// at shutdown. The supervisor itself doesn't own the registry, so the
// config wrapper is the right place to wire teardown.
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

// buildStoreRegistry constructs the supervisor's store registry from the
// (factories, stores) config pair. Returns an empty registry — never nil —
// when both inputs are empty (matches the supervisor.Start contract that a
// non-nil StoreRegistry is required even when no stores are configured).
func buildStoreRegistry(factories []store.Factory, cfg store.StoresConfig) (*store.Registry, error) {
	reg := store.NewRegistry()
	for _, f := range factories {
		reg.Register(f)
	}
	if len(cfg.Stores) == 0 {
		return reg, nil
	}
	if _, err := reg.BuildAll(cfg); err != nil {
		return nil, err
	}
	return reg, nil
}
