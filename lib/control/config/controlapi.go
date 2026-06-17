// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
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
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin"
)

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
	// @deliberate: ControlAPIConfig deliberately carries no Auth field —
	// the auth model is data-derived (active-status predicate on
	// rimsky_api_keys), not yml-config-derived. The control-api constructs
	// its *controlapi.AuthState internally from the persistence handle.
	Stores RemoteStoresConfig
	// NamedLocks is the operator-side named-lock config. The control-
	// api consults this at template-deploy time to validate that every
	// template-referenced lock name is declared.
	NamedLocks locks.NamedLocksConfig
	// Executors is the operator-side executors block. The control-api
	// consults this at template registration to validate that every
	// node-referenced executor name is declared.
	Executors ExecutorsConfig
	// Publishers is the operator-side publishers block. The control-api
	// uses this at publisher-subscription dispatch time to look up the
	// per-publisher gRPC endpoint.
	Publishers RemotePublishersConfig
	// Metrics is the prometheus instrumentation hook. Threaded into
	// controlapi.AppDeps.Metrics so admin-fired invalidates increment
	// `rimsky_invalidates_total{source="admin"}`. Optional; nil → no-op.
	// Production wiring constructs an observability.RegistryHook from
	// the per-process MetricsRegistry.
	Metrics runtime.MetricsHook
	// LateBindServiceProxies maps protocol name → proxy service name,
	// consulted by LifecyclePeersForSpec to add the proxy peer to the
	// fan-out when a template declares late_bind_services.
	LateBindServiceProxies map[string]string
	// RefValidationMode is the operator-set registration-time
	// reference-validation mode (all / available / none). Zero value
	// (node.RefValidateAll) is the strict default. Story
	// S-template-validation-ref-validation-mode.
	RefValidationMode node.RefValidationMode
}

type ControlAPIHandle interface {
	Shutdown(ctx context.Context) error
	Addr() string
	// @agent-contract: ServeErr surfaces a fatal post-start failure of
	// the HTTP serve loop (anything other than a graceful Shutdown). At
	// most one error is ever sent; a clean shutdown sends nothing.
	// Callers that supervise the role (the unified entrypoint, the role
	// mains) select on it to exit non-zero instead of running on
	// degraded.
	ServeErr() <-chan error
}

type controlAPIHandle struct {
	srv             *http.Server
	addr            string
	serveErr        chan error
	registry        *locks.Registry
	lifecycleReg    *locks.LifecycleRegistry
	terminator      *controlapi.InstanceTerminator
	cancelLoops     context.CancelFunc
	cancelDiscovery context.CancelFunc
	// peerClosers releases any non-locks gRPC clients (sensors,
	// validators, data-processing producers) dialed at startup.
	peerClosers []func()
}

func (h *controlAPIHandle) Shutdown(ctx context.Context) error {
	var err error
	if h.srv != nil {
		err = h.srv.Shutdown(ctx)
	}
	// @constraint: audit rows are written synchronously in the request
	// goroutine (controlapi.AuthState.insertEvent), so by the time
	// srv.Shutdown returns every in-flight handler has already persisted
	// its audit row — no dispatcher to stop.
	if h.cancelLoops != nil {
		h.cancelLoops()
	}
	if h.cancelDiscovery != nil {
		h.cancelDiscovery()
	}
	// @constraint: close the store registry before waiting for the
	// terminator. Any in-flight RPCs surface gRPC "connection closed"
	// errors, the terminator's tickBudget bounds it from outside, and
	// Stop's stopBudget bounds the join. This ordering prevents a wedged
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

func (h *controlAPIHandle) ServeErr() <-chan error { return h.serveErr }

// resyncPublishersAtStartup is the startup publisher-subscription
// reconciliation hook. Production points it at
// runtime.ResyncPublisherSubscriptions (re-issue dropped subs, tear down
// orphans). It is a package-level var so the wiring test can override it to
// inject a fake publisher registry and assert StartControlAPI fires resync.
var resyncPublishersAtStartup = runtime.ResyncPublisherSubscriptions

// runPublisherSubscriptionReconciler is the background worker that
// drives the publisher Subscribe handshake for `mounting` subscription
// rows (no attempt cap — see runtime.RunPublisherSubscriptionReconciler).
// Package-level var so the wiring test can override it and assert
// StartControlAPI starts the worker.
var runPublisherSubscriptionReconciler = runtime.RunPublisherSubscriptionReconciler

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
			TLS:                   e.TLS,
		})
	}
	storePeers := make([]observability.PeerSpec, 0, len(cfg.Stores.Stores))
	for name, e := range cfg.Stores.Stores {
		storePeers = append(storePeers, observability.PeerSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
			TLS:                   e.TLS,
		})
	}
	obsLogger := slogLoggerFor(cfg.Logger)
	disc := observability.RunHandshake(context.Background(), observability.NewGRPCProber(), execPeers, storePeers, obsLogger)
	discoveryCtx, cancelDiscovery := context.WithCancel(context.Background())
	go disc.RefreshLoop(discoveryCtx, ObservabilityRefreshInterval(), obsLogger)
	// @constraint: dial the per-protocol registries any peer advertised
	// in its `protocols:` block (publisher / validation /
	// data_processing). Each registry is non-nil even when no peer
	// advertises the protocol — controlapi treats nil and empty
	// registries identically downstream.
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
	// @constraint: in-process bridge for the rotation-grace sweep —
	// when sweep runs in the same process as this AuthState (lifecycle
	// tests and any future single-binary deploy), drop the anonymous-
	// mode cache after each successful sweep. The cross-process case
	// (sweep in scheduler, controlapi in its own process) accepts the
	// TTL-bounded staleness. The unregister closure is dropped
	// intentionally — the hook fires for every sweep until process exit.
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
		// @deliberate: ExecutorCapabilities exposes the observability
		// discovery cache's per-executor (declared_tags,
		// declared_error_classes, expected_attributes_schema) to the
		// controlapi templates registration validator. The cache is
		// already populated by RunHandshake at startup and refreshed by
		// the RefreshLoop goroutine started above; this hook is a thin
		// read-only adapter so templates.go can validate at registration
		// without taking a direct observability import dependency.
		ExecutorCapabilities: func(executorName string) ([]string, []string, []byte, bool) {
			peer, ok := disc.GetExecutor(executorName)
			if !ok || peer.Capabilities == nil {
				return nil, nil, nil, false
			}
			return peer.Capabilities.DeclaredTags,
				peer.Capabilities.DeclaredErrorClasses,
				peer.Capabilities.ExpectedAttributesSchema,
				true
		},
		// @deliberate: StoreDeclaredErrorClasses exposes the discovery
		// cache's per-store producer-declared error-class vocabulary
		// (captured from the ClaimProducer.Capabilities handshake) to
		// the controlapi templates registration validator — the producer
		// half of the executor ∪ producer ∪ acquire/* union the
		// `error_types:` range-check accepts.
		StoreDeclaredErrorClasses: func(storeName string) ([]string, bool) {
			peer, ok := disc.GetStore(storeName)
			if !ok || peer.Capabilities == nil {
				return nil, false
			}
			return peer.Capabilities.DeclaredErrorClasses, true
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
		// @deliberate: late-bind proxy fan-out — threaded so
		// LifecyclePeersForSpec can add the proxy peer when a template
		// declares late_bind_services.
		LateBindServiceProxies: cfg.LateBindServiceProxies,
		// @constraint: operator-set registration-time
		// reference-validation mode (all / available / none) stamped
		// onto the validator hooks so registration + POST
		// /templates/validate share one strictness.
		RefValidationMode: cfg.RefValidationMode,
		// @deliberate: Kind sugar map seeded with the same package
		// constants the supervisor uses so `kind: loop_counter`
		// resolves on the control-API validation path. The map is
		// process-local; in a split-process deploy the supervisor
		// maintains its own copy from the same constants.
		KindAliases: buildKindAliases(),
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
		serveErr:        make(chan error, 1),
		registry:        registry,
		lifecycleReg:    lifecycleReg,
		terminator:      terminator,
		cancelLoops:     cancelLoops,
		cancelDiscovery: cancelDiscovery,
		peerClosers:     peerClosers,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			if cfg.Logger != nil {
				cfg.Logger.Error("controlapi serve", "error", err.Error())
			}
			h.serveErr <- err
		}
	}()
	go terminator.Run(loopCtx)
	go controlapi.WatchAnonymousMode(loopCtx, authState, controlapi.DefaultBannerInterval)
	// @deliberate: reconcile publisher subscriptions against the live
	// publisher peers at startup — re-issue subs rimsky persisted as
	// active but the publisher dropped (e.g. publisher restart), tear
	// down orphan subs the publisher reports for instances rimsky no
	// longer tracks. The publisher registry lives in the control-api
	// (the supervisor never holds one), so this is the correct home for
	// resync. Run in a goroutine, best-effort, log-and-continue on error
	// (matching the sweep discipline): a slow or unreachable publisher
	// must not delay control-api from serving traffic, and one broken
	// publisher cannot wedge startup. Bound by loopCtx so shutdown
	// cancels it.
	resyncLog := cfg.Logger
	if resyncLog == nil {
		resyncLog = shared.SilentLogger{}
	}
	publisherDeps := runtime.PublisherLifecycleDeps{
		Persist:    persistStore,
		Publishers: publisherReg,
		Clock:      cfg.Clock,
		Logger:     resyncLog,
	}
	go func() {
		if err := resyncPublishersAtStartup(loopCtx, publisherDeps); err != nil {
			resyncLog.Warn("controlapi.publisher_resync.failed", "error", err.Error())
		}
	}()
	// @constraint: publisher-subscription reconciler drives the
	// Subscribe handshake for rows instance-create persisted in
	// `mounting` — retry-forever (the tick is the backoff), `failed`
	// reserved for non-retryable errors. Same registry + persistence
	// handles as resync; bound by loopCtx so shutdown stops it.
	go runPublisherSubscriptionReconciler(loopCtx, publisherDeps,
		runtime.DefaultPublisherSubscriptionReconcileInterval)
	return h, nil
}

// buildKindAliases seeds the per-process `kind:` → executor-alias map
// for every rimsky-bundled inproc utility executor via the shared
// `builtin.RegisterAllKindAliases` helper. The supervisor and the
// control-API both call into the same helper (supervisor consumes the
// full `builtin.RegisterAll`, control-API consumes only the alias
// half), so a new bundled executor lands by editing
// `lib/runtime/executor/builtin/builtins.go` — both wiring sites pick
// it up automatically.
//
// The control-API process runs validatorHooksFor on every template
// registration; it consults this map to range-check `kind:` and to
// drive the kind→executor canonicalization step.
//
// Panics on registration failure (a duplicate seed against a freshly-
// constructed map is a startup invariant violation, not a runtime
// recovery point).
//
// @concept: node
func buildKindAliases() *node.KindAliasMap {
	m := node.NewKindAliasMap()
	if err := builtin.RegisterAllKindAliases(m); err != nil {
		panic(fmt.Sprintf("controlapi: build kind aliases: %v", err))
	}
	return m
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
