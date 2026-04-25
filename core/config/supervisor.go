// Plan A Task 10.6 — thin entry-point wrapper over supervisor.Start.
//
// Public API for embedding a rimsky supervisor into a host process.
package config

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
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
	// GetResource builds a Resource for a given resource row. Typically
	// wraps a FactoryRegistry.Get(impl).Create(...) lookup.
	GetResource func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error)
	// ResourceFactories is the explicit factory registry used by the
	// supervisor (template validation + GetResource callbacks). If nil,
	// resource.DefaultRegistry() is used — this preserves backward-compat
	// for callers still relying on resource.RegisterFactory. New code
	// should construct a per-process *resource.FactoryRegistry.
	ResourceFactories *resource.FactoryRegistry
	ConcurrencyLimits map[string]int
	// SQLConnections is a named-pool map for external-sql resources (Plan C).
	// Plan A uses inline-jsonb only; this field can be left nil for v1.
	SQLConnections map[string]*pgxpool.Pool
	CallbackHost   string
	CallbackPort   int
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
	if cfg.GetResource == nil {
		return nil, fmt.Errorf("StartSupervisor: GetResource required")
	}
	factories := cfg.ResourceFactories
	if factories == nil {
		factories = resource.DefaultRegistry()
	}
	return supervisor.Start(supervisor.Config{
		SupervisorID:          cfg.SupervisorID,
		Storage:               cfg.Storage,
		Queue:                 cfg.Queue,
		Clock:                 cfg.Clock,
		Logger:                cfg.Logger,
		Concurrency:           cfg.Concurrency,
		HeartbeatInterval:     cfg.HeartbeatInterval,
		ClaimPollInterval:     cfg.ClaimPollInterval,
		Resolver:              cfg.Resolver,
		GetResource:           cfg.GetResource,
		ResourceFactories:     factories,
		CallbackHost:          cfg.CallbackHost,
		CallbackPort:          cfg.CallbackPort,
		CallbackAdvertiseHost: cfg.CallbackAdvertiseHost,
		CallbackAdvertisePort: cfg.CallbackAdvertisePort,
	})
}
