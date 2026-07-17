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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type ControlAPIConfig struct {
	Driver                 persistence.Database
	Clock                  shared.Clock
	Logger                 shared.Logger
	Host                   string
	Port                   int
	ClaimProducers         RemoteClaimProducersConfig
	NamedLocks             locks.NamedLocksConfig
	Executors              ExecutorsConfig
	Publishers             RemotePublishersConfig
	Metrics                runtime.MetricsHook
	LateBindServiceProxies map[string]string
	RefValidationMode      node.RefValidationMode
	PeerAuth               string

	Bundled *BundledRegistrations
}

type ControlAPIHandle interface {
	Shutdown(ctx context.Context) error
	Addr() string
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
	peerClosers     []func()
}

func (h *controlAPIHandle) Shutdown(ctx context.Context) error {
	var err error
	if h.srv != nil {
		err = h.srv.Shutdown(ctx)
	}
	if h.cancelLoops != nil {
		h.cancelLoops()
	}
	if h.cancelDiscovery != nil {
		h.cancelDiscovery()
	}
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

var resyncPublishersAtStartup = runtime.ResyncPublisherSubscriptions

var runPublisherSubscriptionReconciler = runtime.RunPublisherSubscriptionReconciler

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
	registry, err := dialRemoteClaimProducers(context.Background(), cfg.ClaimProducers, persistStore, cfg.LateBindServiceProxies)
	if err != nil {
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	if cfg.Bundled != nil {
		obsLog := slogLoggerFor(cfg.Logger)
		for name, producer := range cfg.Bundled.ClaimProducerClients() {
			if _, exists := registry.Get(name); exists {
				obsLog.Info("bundled claim producer overridden by configured endpoint", "producer", name)
				continue
			}
			registry.Add(name, producer)
		}
	}
	lifecycleReg, err := DialLifecycleSubscribers(context.Background(), cfg.ClaimProducers, cfg.Executors)
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
	if cfg.Bundled != nil {
		for name, ep := range cfg.Bundled.ExecutorAliases {
			if _, exists := executorsByName[name]; exists {
				continue
			}
			executorsByName[name] = controlapi.ExecutorEntry{
				Transport: ep.Transport,
				Endpoint:  ep.URL,
			}
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
	storePeers := make([]observability.PeerSpec, 0, len(cfg.ClaimProducers.ClaimProducers))
	for name, e := range cfg.ClaimProducers.ClaimProducers {
		storePeers = append(storePeers, observability.PeerSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
			TLS:                   e.TLS,
		})
	}
	obsLogger := slogLoggerFor(cfg.Logger)
	disc := observability.RunHandshake(context.Background(), observability.NewGRPCProber(), execPeers, storePeers, obsLogger)
	if cfg.Bundled != nil {
		cfg.Bundled.AdvertiseInto(disc)
	}
	discoveryCtx, cancelDiscovery := context.WithCancel(context.Background())
	go disc.RefreshLoop(discoveryCtx, ObservabilityRefreshInterval(), obsLogger)
	publisherReg, validationReg, dataProcessorReg, peerClosers, err := DialPublisherAndValidationRegistries(context.Background(), cfg.ClaimProducers, cfg.Executors, cfg.Publishers)
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
	_ = runtime.RegisterAuthMutationHook(authState.OnAuthMutation)
	var enrollDeps *controlapi.EnrollDeps
	if cfg.PeerAuth == peer.PeerAuthMTLS {
		ca, err := ensureDeploymentCA(context.Background(), persistStore, cfg.Clock)
		if err != nil {
			cancelDiscovery()
			registry.Close()
			lifecycleReg.Close()
			for _, c := range peerClosers {
				c()
			}
			return nil, fmt.Errorf("StartControlAPI: %w", err)
		}
		enrollDeps = &controlapi.EnrollDeps{CA: ca, LeafTTL: pki.LeafTTL, Clock: cfg.Clock}
	}
	deps := controlapi.AppDeps{
		Persist:        persistStore,
		Queue:          persistQueue,
		Clock:          cfg.Clock,
		Logger:         cfg.Logger,
		AuthState:      authState,
		ClaimProducers: registry,
		LifecycleSubs:  lifecycleReg,
		NamedLocks:     cfg.NamedLocks,
		Executors:      executorsByName,
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
		StoreDeclaredErrorClasses: func(storeName string) ([]string, bool) {
			peer, ok := disc.GetStore(storeName)
			if !ok || peer.Capabilities == nil {
				return nil, false
			}
			return peer.Capabilities.DeclaredErrorClasses, true
		},
		Observability: func(r chi.Router) {
			observability.Routes(r, observability.Deps{
				Tables:         persistStore,
				Queue:          persistQueue,
				Driver:         cfg.Driver,
				Executors:      execPeers,
				ClaimProducers: storePeers,
				Discovery:      disc,
			})
		},
		Metrics:                cfg.Metrics,
		Publishers:             publisherReg,
		Validators:             validationReg,
		DataProcessors:         dataProcessorReg,
		LateBindServiceProxies: cfg.LateBindServiceProxies,
		RefValidationMode:      cfg.RefValidationMode,
		KindAliases:            buildKindAliases(),
		PeerAuth:               cfg.PeerAuth,
		Enroll:                 enrollDeps,
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
	go runPublisherSubscriptionReconciler(loopCtx, publisherDeps,
		runtime.DefaultPublisherSubscriptionReconcileInterval)
	return h, nil
}

// @concept: node
func buildKindAliases() *node.KindAliasMap {
	m := node.NewKindAliasMap()
	if err := builtin.RegisterAllKindAliases(m); err != nil {
		panic(fmt.Sprintf("controlapi: build kind aliases: %v", err))
	}
	return m
}

func slogLoggerFor(l shared.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return slog.New(&sharedLoggerHandler{l: l})
}

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

func ObservabilityRefreshInterval() time.Duration {
	if v := os.Getenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 60 * time.Second
}
