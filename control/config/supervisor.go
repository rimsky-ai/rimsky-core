// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Public API for embedding a rimsky supervisor into a host process.
package config

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/runtime"
	"github.com/rimsky-ai/rimsky-core/runtime/executor"
)

// SupervisorConfig wires a supervisor process. Per spec §6.1 — the
// stores config is a thin "name → endpoint + declared capabilities"
// form; the supervisor dials each entry and validates the
// Capabilities() handshake at startup.
type SupervisorConfig struct {
	SupervisorID string
	// Driver is the unified persistence driver. Required.
	Driver            persistence.Database
	Clock             shared.Clock
	Logger            shared.Logger
	Concurrency       int
	HeartbeatInterval time.Duration
	ClaimPollInterval time.Duration
	Resolver          executor.Resolver
	// Stores is the parsed `stores:` block from rimsky.yml: an
	// endpoint URL plus declared capabilities per entry.
	Stores RemoteStoresConfig
	// NamedLocks is the operator-side named-lock config. Empty /
	// missing → no named locks declared; templates that reference
	// named locks will fail registry-dependent validation.
	NamedLocks   locks.NamedLocksConfig
	CallbackHost string
	CallbackPort int
	// CallbackAdvertiseHost / CallbackAdvertisePort override the
	// host:port embedded in the `callback_url` handed to executors.
	CallbackAdvertiseHost string
	CallbackAdvertisePort int

	// Blob is the active BlobBackend; threaded into the integration
	// runtime so named-event and parked-payload writes can spill.
	// Nil = no spill at those sites (the attribute persistence path
	// uses the same backend via the driver's Store, configured via
	// Driver.SetBlobBackend at startup).
	Blob persistence.BlobBackend
	// BlobSpillThreshold is the spill cutoff in bytes; zero disables.
	BlobSpillThreshold int
	// ExpectedAttributesSchemaFor returns the named executor's
	// advertised expected_attributes_schema bytes (JSON Schema).
	// Optional. When set, the supervisor threads it through to the
	// runtime so dispatch-time effective-schema computation can merge
	// the executor's contribution against L1 template defaults and L2
	// per-node declarations. Production wiring constructs this from
	// observability.NewExpectedAttributesSchemaResolver(disc).
	//
	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	// Metrics is the prometheus instrumentation hook (plan I2).
	// Optional; nil → no-op everywhere. Production wiring constructs an
	// observability.RegistryHook from the per-process MetricsRegistry.
	Metrics runtime.MetricsHook
	// MaxParkDuration is the deployment-level per-reason max_park_duration
	// cap map. Threaded into the SweepParkedNodes path so the watchdog
	// can fail parked runs that overrun their per-reason cap even when
	// the per-row col:rimsky_node_runs.max_park_duration_seconds is NULL.
	// Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Parked-state taxonomy. Empty / nil → only per-row caps fire.
	MaxParkDuration map[string]time.Duration

	// LifecyclePeersForSpec returns the lifecycle peer names that should
	// receive instance- and run-scope-keyed events for a given template
	// spec. Production wiring supplies a closure that calls
	// controlapi.LifecyclePeersForSpec with the rimsky.yml
	// late_bind_service_proxies map baked in. The closure lives in the
	// control/ layer (cmd/rimsky-supervisor/main.go) so runtime/ never
	// imports control/ (denied by .golangci.yml's runtime-purity rule).
	//
	// Per spec 2026-05-24-host-agent-and-proxy-design.md.
	LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string

	// LifecycleSubs is the supervisor's outbound LifecycleSubscriber
	// registry. Populated by StartSupervisor via DialLifecycleSubscribers
	// (the same helper control-api uses). Used to fire OnRunScopeTerminal
	// for sub-graph and fanout-partition scope closes.
	LifecycleSubs *locks.LifecycleRegistry

	// Executors mirrors the rimsky.yml executors: block. The supervisor
	// needs it for DialLifecycleSubscribers (which walks the union of
	// claim_producers: + executors: looking for peers whose protocols:
	// list includes lifecycle_subscriber). Existing supervisor wiring
	// already takes Stores but not Executors; adding this field is a
	// prerequisite for the DialLifecycleSubscribers call.
	Executors ExecutorsConfig

	// LateBindServiceProxies passes the rimsky.yml late-bind map through
	// to the supervisor for use in SelectCandidatesRequest construction
	// and Registry option wiring (protocol name → proxy service name).
	LateBindServiceProxies map[string]string
}

// SupervisorHandle is the lifecycle handle returned by StartSupervisor.
type SupervisorHandle interface {
	Shutdown(ctx context.Context) error
	CallbackAddr() string
	// CallbackRegistry exposes the supervisor's callback registry for
	// test-only callers (the F4 callback-determinism scenario; see
	// runtime.Handle.CallbackRegistry doc). Production callers should
	// NOT reach into the registry directly.
	CallbackRegistry() *runtime.CallbackRegistry
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
	if cfg.Driver == nil {
		return nil, fmt.Errorf("StartSupervisor: Driver required")
	}
	persistStore := cfg.Driver.Tables()
	if persistStore == nil {
		return nil, fmt.Errorf("StartSupervisor: Database.Tables() returned nil — driver did not initialize the Tables accessor")
	}
	registry, err := dialRemoteStores(context.Background(), cfg.Stores, persistStore, cfg.LateBindServiceProxies)
	if err != nil {
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	// Dial the supervisor's outbound LifecycleSubscriber peers (the same
	// helper control-api uses). The supervisor fires OnRunScopeTerminal
	// to these peers at sub-graph and fanout-partition scope closes.
	lifecycleSubs, err := DialLifecycleSubscribers(context.Background(), cfg.Stores, cfg.Executors)
	if err != nil {
		registry.Close()
		return nil, fmt.Errorf("StartSupervisor: dial lifecycle subscribers: %w", err)
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		registry.Close()
		lifecycleSubs.Close()
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	persistQueue := cfg.Driver.Queue()
	if persistQueue == nil {
		registry.Close()
		lifecycleSubs.Close()
		return nil, fmt.Errorf("StartSupervisor: Driver.Queue() returned nil")
	}
	coordinator := cfg.Driver.AdvisoryLocker()
	if coordinator == nil {
		registry.Close()
		lifecycleSubs.Close()
		return nil, fmt.Errorf("StartSupervisor: Driver.AdvisoryLocker() returned nil")
	}
	inner, err := runtime.Start(runtime.Config{
		SupervisorID:                cfg.SupervisorID,
		Persist:                     persistStore,
		Queue:                       persistQueue,
		AdvisoryLocker:              coordinator,
		Clock:                       cfg.Clock,
		Logger:                      cfg.Logger,
		Concurrency:                 cfg.Concurrency,
		HeartbeatInterval:           cfg.HeartbeatInterval,
		ClaimPollInterval:           cfg.ClaimPollInterval,
		Resolver:                    cfg.Resolver,
		StoreRegistry:               registry,
		NamedLocks:                  cfg.NamedLocks,
		CallbackHost:                cfg.CallbackHost,
		CallbackPort:                cfg.CallbackPort,
		CallbackAdvertiseHost:       cfg.CallbackAdvertiseHost,
		CallbackAdvertisePort:       cfg.CallbackAdvertisePort,
		Blob:                        cfg.Blob,
		BlobSpillThreshold:          cfg.BlobSpillThreshold,
		ExpectedAttributesSchemaFor: cfg.ExpectedAttributesSchemaFor,
		Metrics:                     cfg.Metrics,
		MaxParkDuration:             cfg.MaxParkDuration,
		LifecycleSubs:               lifecycleSubs,
		LifecyclePeersForSpec:       cfg.LifecyclePeersForSpec,
		LateBindServiceProxies:      cfg.LateBindServiceProxies,
	})
	if err != nil {
		registry.Close()
		lifecycleSubs.Close()
		return nil, err
	}
	return supervisorHandleWithRegistry{inner: inner, registry: registry, lifecycleSubs: lifecycleSubs}, nil
}

// supervisorHandleWithRegistry wraps runtime.Handle to release the
// remote-store + lifecycle-subscriber gRPC connections at shutdown.
type supervisorHandleWithRegistry struct {
	inner         SupervisorHandle
	registry      *locks.Registry
	lifecycleSubs *locks.LifecycleRegistry
}

func (h supervisorHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	h.registry.Close()
	if h.lifecycleSubs != nil {
		h.lifecycleSubs.Close()
	}
	return err
}

func (h supervisorHandleWithRegistry) CallbackAddr() string {
	return h.inner.CallbackAddr()
}

func (h supervisorHandleWithRegistry) CallbackRegistry() *runtime.CallbackRegistry {
	return h.inner.CallbackRegistry()
}
