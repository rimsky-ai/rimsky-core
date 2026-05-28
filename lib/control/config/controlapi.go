// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// controlapiInvalidateAdapter wraps the runtime
// InvalidateAdapter so it returns the controlapi.ErrInvalidateConflict
// sentinel when the foundation runtime reports the target is running.
// Without this translation the admin handler would 500 instead of 409
// because errors.Is on the foundation's ErrInvalidateRunning sentinel
// would fail (the controlapi handler only knows ErrInvalidateConflict).
type controlapiInvalidateAdapter struct {
	inner *runtime.InvalidateAdapter
}

func (a *controlapiInvalidateAdapter) InvalidateNode(ctx context.Context, instanceID, nodeID string) (any, error) {
	out, err := a.inner.InvalidateNode(ctx, instanceID, nodeID)
	if err != nil {
		if errors.Is(err, runtime.ErrInvalidateRunning) {
			return nil, fmt.Errorf("%w: %v", controlapi.ErrInvalidateConflict, err)
		}
		return nil, err
	}
	return out, nil
}

// controlapiSupervisorID returns the supervisor id stamped on
// audit-log rows for control-api-originated wakes. Defaults to a
// hostname-derived value so multi-replica deployments don't collide.
// Override with RIMSKY_CONTROLAPI_ID.
func controlapiSupervisorID() string {
	if v := os.Getenv("RIMSKY_CONTROLAPI_ID"); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return "control-api-" + h
	}
	return "control-api"
}

// ControlAPIConfig wires the control-api HTTP server. The store config
// follows the same name → endpoint + capabilities shape as the
// supervisor and scheduler. Per spec §6.1.
type ControlAPIConfig struct {
	// Driver is the unified persistence driver. Required.
	Driver persistence.Database
	Clock  shared.Clock
	Logger shared.Logger
	Host   string
	Port   int
	// Auth is no longer carried in ControlAPIConfig — the auth model
	// is data-derived (the active-status predicate on
	// `rimsky_api_keys`) and not yml-config-derived. The control-api
	// constructs its `*controlapi.AuthState` internally from the
	// persistence handle.
	Stores RemoteStoresConfig
	// NamedLocks is the operator-side named-lock config. The control-
	// api consults this at template-deploy time to validate that
	// every template-referenced lock name is declared.
	NamedLocks locks.NamedLocksConfig
	// Executors is the operator-side executors block from rimsky.yml
	// (per docs/specs/2026-05-01-control-plane-and-store-lifecycle-
	// design.md §3.1). The control-api consults this at template
	// registration to validate that every node-referenced executor
	// name is declared.
	Executors ExecutorsConfig
	// Publishers is the operator-side publishers block from rimsky.yml
	// (per spec
	// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md).
	// The control-api uses this at publisher-subscription dispatch
	// time to look up the per-publisher gRPC endpoint.
	Publishers RemotePublishersConfig
	// Metrics is the prometheus instrumentation hook (plan I2).
	// Threaded into controlapi.AppDeps.Metrics so admin-fired
	// invalidates increment `rimsky_invalidates_total{source="admin"}`.
	// Optional; nil → no-op. Production wiring constructs an
	// observability.RegistryHook from the per-process MetricsRegistry.
	Metrics runtime.MetricsHook
	// LateBindServiceProxies maps protocol name → proxy service name,
	// passed verbatim from rimsky.yml's late_bind_service_proxies into
	// controlapi.AppDeps. Consulted by LifecyclePeersForSpec to add the
	// proxy peer to the fan-out when a template declares late_bind_services.
	LateBindServiceProxies map[string]string
}

type ControlAPIHandle interface {
	Shutdown(ctx context.Context) error
	Addr() string
}

type controlAPIHandle struct {
	srv             *http.Server
	addr            string
	registry        *locks.Registry
	lifecycleReg    *locks.LifecycleRegistry
	terminator      *controlapi.InstanceTerminator
	cancelLoops     context.CancelFunc
	cancelDiscovery context.CancelFunc
	// peerClosers releases any non-locks gRPC clients (sensors,
	// validators, data-processing producers) dialed at startup.
	peerClosers []func()
	// authState is retained so Shutdown can stop the audit dispatcher
	// after the HTTP server has drained — handler responses that fire
	// audit submissions on their way out must still find an open queue.
	authState *controlapi.AuthState
}

func (h *controlAPIHandle) Shutdown(ctx context.Context) error {
	var err error
	if h.srv != nil {
		err = h.srv.Shutdown(ctx)
	}
	// Stop the audit dispatcher AFTER srv.Shutdown returns so any
	// in-flight handler responses still enqueue their final audit rows
	// before the channel closes. Stop closes the queue and waits for
	// the workers to drain, so any queued rows are persisted before
	// process exit. Without this the four dispatcher goroutines would
	// leak and queued rows would be silently discarded.
	if h.authState != nil {
		h.authState.StopAuditDispatcher()
	}
	if h.cancelLoops != nil {
		h.cancelLoops()
	}
	if h.cancelDiscovery != nil {
		h.cancelDiscovery()
	}
	// Close the store registry before waiting for the terminator: any
	// in-flight RPCs surface gRPC "connection closed" errors, the
	// terminator's tickBudget bounds it from outside, and Stop's
	// stopBudget bounds the join. This ordering prevents a wedged
	// store RPC from blocking process shutdown forever.
	if h.registry != nil {
		h.registry.Close()
	}
	if h.lifecycleReg != nil {
		h.lifecycleReg.Close()
	}
	for _, c := range h.peerClosers {
		c()
	}
	if h.terminator != nil {
		h.terminator.Stop()
	}
	return err
}
func (h *controlAPIHandle) Addr() string { return h.addr }

// StartControlAPI binds host:port (port=0 for OS-assigned) and starts
// serving.
func StartControlAPI(cfg ControlAPIConfig) (ControlAPIHandle, error) {
	if cfg.Driver == nil {
		return nil, fmt.Errorf("StartControlAPI: Driver is required")
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	persistStore := cfg.Driver.Tables()
	if persistStore == nil {
		return nil, fmt.Errorf("StartControlAPI: Database.Tables() returned nil — driver did not initialize the Tables accessor")
	}
	persistQueue := cfg.Driver.Queue()
	if persistQueue == nil {
		return nil, fmt.Errorf("StartControlAPI: Driver.Queue() returned nil")
	}
	registry, err := dialRemoteStores(context.Background(), cfg.Stores, persistStore, cfg.LateBindServiceProxies)
	if err != nil {
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	lifecycleReg, err := DialLifecycleSubscribers(context.Background(), cfg.Stores, cfg.Executors)
	if err != nil {
		registry.Close()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		registry.Close()
		lifecycleReg.Close()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	if err := cfg.Executors.Validate(); err != nil {
		registry.Close()
		lifecycleReg.Close()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	executorsByName := make(map[string]controlapi.ExecutorEntry, len(cfg.Executors.Executors))
	for name, e := range cfg.Executors.Executors {
		executorsByName[name] = controlapi.ExecutorEntry{
			Transport: e.Transport,
			Endpoint:  e.Endpoint,
			TLS:       e.TLS,
		}
	}
	execPeers := make([]observability.PeerSpec, 0, len(cfg.Executors.Executors))
	for name, e := range cfg.Executors.Executors {
		execPeers = append(execPeers, observability.PeerSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		})
	}
	storePeers := make([]observability.PeerSpec, 0, len(cfg.Stores.Stores))
	for name, e := range cfg.Stores.Stores {
		storePeers = append(storePeers, observability.PeerSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		})
	}
	obsLogger := slogLoggerFor(cfg.Logger)
	disc := observability.RunHandshake(context.Background(), observability.NewGRPCProber(), execPeers, storePeers, obsLogger)
	discoveryCtx, cancelDiscovery := context.WithCancel(context.Background())
	go disc.RefreshLoop(discoveryCtx, ObservabilityRefreshInterval(), obsLogger)
	// Dial the per-protocol registries any peer advertised in its
	// `protocols:` block (publisher / validation / data_processing).
	// Each registry is non-nil even when no peer advertises the
	// protocol — controlapi treats nil and empty registries identically
	// downstream.
	publisherReg, validationReg, dataProcessorReg, peerClosers, err := DialPublisherAndValidationRegistries(context.Background(), cfg.Stores, cfg.Executors, cfg.Publishers)
	if err != nil {
		cancelDiscovery()
		registry.Close()
		lifecycleReg.Close()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	authState := &controlapi.AuthState{
		Tables:   persistStore,
		Registry: controlapi.BuildV1Registry(),
		Clock:    cfg.Clock,
		Logger:   cfg.Logger,
	}
	// Wire the audit-dispatcher worker pool so audit-row inserts run
	// off the request hot path. Without this, insertEvent falls
	// back to a synchronous insert per row — fine for tests, but in
	// production a slow Postgres would hold request goroutines open
	// past response write.
	authState.EnsureAuditDispatcher()
	// In-process bridge for the rotation-grace sweep: when sweep
	// runs in the same process as this AuthState (lifecycle tests
	// and any future single-binary deploy), drop the anonymous-
	// mode cache after each successful sweep. The cross-process
	// case (sweep in scheduler, controlapi in its own process)
	// accepts the TTL-bounded staleness per spec.
	// Production wiring keeps the registration for the life of the
	// process. The unregister closure is dropped intentionally — the
	// hook should fire for every sweep until process exit.
	_ = runtime.RegisterAuthMutationHook(authState.OnAuthMutation)
	deps := controlapi.AppDeps{
		Persist:       persistStore,
		Queue:         persistQueue,
		Clock:         cfg.Clock,
		Logger:        cfg.Logger,
		AuthState:     authState,
		Stores:        registry,
		LifecycleSubs: lifecycleReg,
		NamedLocks:    cfg.NamedLocks,
		Executors:     executorsByName,
		// Plan G3 / E3 / H2: control-api delegates admin
		// invalidates to the foundation runtime's UnifiedInvalidate via
		// this adapter; same code path that the parked-nodes sweep and
		// the on_event handler dispatch use, so handler-emitted
		// invalidates correctly resume parked targets.
		InvalidateHandler: &controlapiInvalidateAdapter{
			inner: &runtime.InvalidateAdapter{
				Persist:      persistStore,
				Queue:        persistQueue,
				Clock:        cfg.Clock,
				Logger:       cfg.Logger,
				SupervisorID: controlapiSupervisorID(),
				Metrics:      cfg.Metrics,
			},
		},
		// Plan F6 / F7 + 2026-05-23 signal-taxonomy Pass 6:
		// ExecutorCapabilities exposes the observability discovery cache's
		// per-executor (declared_events, declared_error_classes,
		// expected_attributes_schema) to the controlapi templates
		// registration validator. The cache is already populated by
		// RunHandshake at startup and refreshed by the RefreshLoop
		// goroutine started above; this hook is a thin read-only adapter
		// so templates.go can validate at registration without taking a
		// direct observability import dependency.
		ExecutorCapabilities: func(executorName string) ([]string, []string, []byte, bool) {
			peer, ok := disc.GetExecutor(executorName)
			if !ok || peer.Capabilities == nil {
				return nil, nil, nil, false
			}
			return peer.Capabilities.DeclaredEvents,
				peer.Capabilities.DeclaredErrorClasses,
				peer.Capabilities.ExpectedAttributesSchema,
				true
		},
		Observability: func(r chi.Router) {
			observability.Routes(r, observability.Deps{
				Tables:    persistStore,
				Queue:     persistQueue,
				Driver:    cfg.Driver,
				Executors: execPeers,
				Stores:    storePeers,
				Discovery: disc,
			})
		},
		Metrics:        cfg.Metrics,
		Publishers:     publisherReg,
		Validators:     validationReg,
		DataProcessors: dataProcessorReg,
		// Plan: late-bind proxy fan-out. Threaded so LifecyclePeersForSpec
		// can add the proxy peer when a template declares late_bind_services.
		LateBindServiceProxies: cfg.LateBindServiceProxies,
	}
	app := controlapi.NewApp(deps)
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		cancelDiscovery()
		registry.Close()
		lifecycleReg.Close()
		for _, c := range peerClosers {
			c()
		}
		return nil, fmt.Errorf("StartControlAPI: listen: %w", err)
	}
	srv := &http.Server{Handler: app}
	terminator := controlapi.NewInstanceTerminator(deps, 0)
	loopCtx, cancelLoops := context.WithCancel(context.Background())
	h := &controlAPIHandle{
		srv:             srv,
		addr:            listener.Addr().String(),
		registry:        registry,
		lifecycleReg:    lifecycleReg,
		terminator:      terminator,
		cancelLoops:     cancelLoops,
		cancelDiscovery: cancelDiscovery,
		peerClosers:     peerClosers,
		authState:       authState,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed && cfg.Logger != nil {
			cfg.Logger.Error("controlapi serve", "error", err.Error())
		}
	}()
	go terminator.Run(loopCtx)
	// Anonymous-mode banner: logs once at startup and every 5
	// minutes thereafter while no API keys exist.
	go controlapi.WatchAnonymousMode(loopCtx, authState, controlapi.DefaultBannerInterval)
	return h, nil
}

// slogLoggerFor coerces the rimsky-style shared.Logger contract into a
// stdlib *slog.Logger for the observability package. The bridge wraps
// the supplied logger in a small slog.Handler adapter so observability
// handshake/refresh log lines route through the configured logger
// rather than the package default. When nil, slog.Default() is used.
func slogLoggerFor(l shared.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return slog.New(&sharedLoggerHandler{l: l})
}

// sharedLoggerHandler adapts shared.Logger to slog.Handler. Passes
// through the level + message + flat key/value fields; ignores group
// nesting and source-location attributes (not used by the
// observability package).
type sharedLoggerHandler struct{ l shared.Logger }

func (h *sharedLoggerHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *sharedLoggerHandler) Handle(_ context.Context, r slog.Record) error {
	fields := make([]any, 0, r.NumAttrs()*2)
	r.Attrs(func(a slog.Attr) bool {
		fields = append(fields, a.Key, a.Value.Any())
		return true
	})
	switch r.Level {
	case slog.LevelDebug:
		h.l.Debug(r.Message, fields...)
	case slog.LevelWarn:
		h.l.Warn(r.Message, fields...)
	case slog.LevelError:
		h.l.Error(r.Message, fields...)
	default:
		h.l.Info(r.Message, fields...)
	}
	return nil
}

func (h *sharedLoggerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	fields := make([]any, 0, len(attrs)*2)
	for _, a := range attrs {
		fields = append(fields, a.Key, a.Value.Any())
	}
	return &sharedLoggerHandler{l: h.l.With(fields...)}
}

func (h *sharedLoggerHandler) WithGroup(_ string) slog.Handler { return h }

// ObservabilityRefreshInterval returns the configured background re-
// probe interval, parsed from RIMSKY_OBSERVABILITY_REFRESH_INTERVAL
// (Go time.Duration syntax). Defaults to 60s per spec §4. Exported so
// supervisor-side wiring (cmd/rimsky-supervisor/main.go) can drive its
// own RefreshLoop on the same env-var contract.
func ObservabilityRefreshInterval() time.Duration {
	if v := os.Getenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 60 * time.Second
}
