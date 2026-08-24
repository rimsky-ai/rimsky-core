// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

type ControlAPIConfig struct {
	Driver                 persistence.Database
	Clock                  shared.Clock
	Logger                 shared.Logger
	Host                   string
	Port                   int
	ControlAPIID           string
	ClaimProducers         RemoteClaimProducersConfig
	NamedLocks             locks.NamedLocksConfig
	Executors              ExecutorsConfig
	Publishers             RemotePublishersConfig
	Validators             RemoteValidatorsConfig
	DataProcessors         RemoteDataProcessorsConfig
	Metrics                runtime.MetricsHook
	LateBindServiceProxies map[string]string
	ServiceAuth            string

	// @concept: validation
	UnreachableValidatorPolicy string

	ObservabilityRefreshInterval time.Duration

	// @decision: lifecycle-subscriber-at-least-once-delivery
	ServiceDeliveryStallAfter time.Duration

	// @decision: lifecycle-drain-per-role
	SharedLifecycleDrain *runtime.LifecycleReconciler

	Bundled *BundledRegistrations

	ResyncPublishers                func(context.Context, runtime.PublisherLifecycleDeps) error
	ReconcilePublisherSubscriptions func(context.Context, runtime.PublisherLifecycleDeps, time.Duration)
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
	lifecycleReg    *lifecycle.Registry
	lifecycleDrain  *runtime.LifecycleReconciler
	cancelLoops     context.CancelFunc
	cancelDiscovery context.CancelFunc
	serviceClosers  []func()
	wg              sync.WaitGroup
}

func (h *controlAPIHandle) goWG(f func()) {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		f()
	}()
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
	if h.lifecycleDrain != nil {
		h.lifecycleDrain.Stop()
	}
	h.wg.Wait()
	if h.registry != nil {
		h.registry.Close()
	}
	if h.lifecycleReg != nil {
		h.lifecycleReg.Close()
	}
	for _, c := range h.serviceClosers {
		c()
	}
	return err
}
func (h *controlAPIHandle) Addr() string { return h.addr }

func (h *controlAPIHandle) ServeErr() <-chan error { return h.serveErr }

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
	var (
		mtlsCA       *pki.CA
		mtlsIdentity *service.IdentityHolder
		stopIdentity = func() {}
	)
	if cfg.ServiceAuth == service.ServiceAuthMTLS {
		principal := cfg.ControlAPIID
		if principal == "" {
			principal = defaultControlAPIPrincipal()
		}
		holder, ca, cancel, err := installServiceIdentity(context.Background(), persistStore, principal, cfg.Clock, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("StartControlAPI: %w", err)
		}
		mtlsCA = ca
		mtlsIdentity = holder
		stopIdentity = cancel
	}
	registry, err := dialRemoteClaimProducers(context.Background(), cfg.ClaimProducers, persistStore, cfg.LateBindServiceProxies)
	if err != nil {
		stopIdentity()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	if cfg.Bundled != nil {
		obsLog := slogLoggerFor(cfg.Logger)
		mergeBundledClaimProducers(registry, cfg.Bundled.ClaimProducerClients(), func(name string) {
			obsLog.Info("BUNDLED.CLAIMPRODUCER.OVERRIDDEN", "producer", name)
		})
	}
	lifecycleReg, err := DialLifecycleSubscribers(context.Background(), cfg.ClaimProducers, cfg.Executors, cfg.Publishers)
	if err != nil {
		stopIdentity()
		registry.Close()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		stopIdentity()
		registry.Close()
		lifecycleReg.Close()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	if err := cfg.Executors.Validate(); err != nil {
		stopIdentity()
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
		mergeBundledExecutorEntries(executorsByName, cfg.Bundled.ExecutorAliases)
	}
	bundledStores := map[string]locks.ClaimProducer{}
	if cfg.Bundled != nil {
		bundledStores = cfg.Bundled.ClaimProducerClients()
	}
	// @concept: service-address-book
	if err := publishServiceAddressBook(context.Background(), persistStore, executorsByName, cfg.ClaimProducers, bundledStores); err != nil {
		stopIdentity()
		registry.Close()
		lifecycleReg.Close()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	execServices := make([]observability.ServiceSpec, 0, len(cfg.Executors.Executors))
	for name, e := range cfg.Executors.Executors {
		execServices = append(execServices, observability.ServiceSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
			TLS:                   e.TLS,
		})
	}
	storeServices := make([]observability.ServiceSpec, 0, len(cfg.ClaimProducers.ClaimProducers))
	for name, e := range cfg.ClaimProducers.ClaimProducers {
		storeServices = append(storeServices, observability.ServiceSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
			TLS:                   e.TLS,
		})
	}
	obsLogger := slogLoggerFor(cfg.Logger)
	// @concept: observability
	disc := observability.RunHandshake(context.Background(), observability.NewGRPCProber(), execServices, storeServices, obsLogger)
	if cfg.Bundled != nil {
		cfg.Bundled.AdvertiseInto(disc, cfg.Executors.Executors, cfg.ClaimProducers.ClaimProducers)
	}
	discoveryCtx, cancelDiscovery := context.WithCancel(context.Background())
	publisherReg, validationReg, dataProcessorReg, serviceClosers, err := DialPublisherAndValidationRegistries(context.Background(), cfg.ClaimProducers, cfg.Executors, cfg.Publishers, cfg.Validators, cfg.DataProcessors)
	if err != nil {
		stopIdentity()
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
	if mtlsCA != nil {
		enrollDeps = &controlapi.EnrollDeps{CA: mtlsCA, LeafTTL: pki.LeafTTL, Clock: cfg.Clock}
	}
	// @decision: lifecycle-drain-per-role
	lifecycleDrain := cfg.SharedLifecycleDrain
	if lifecycleDrain == nil {
		lifecycleDrain = runtime.NewLifecycleReconciler(runtime.LifecycleReconcilerConfig{
			Persist:        persistStore,
			AdvisoryLocker: cfg.Driver.AdvisoryLocker(),
			Subscribers:    lifecycleReg,
			Clock:          cfg.Clock,
			Logger:         cfg.Logger,
			StallAfter:     cfg.ServiceDeliveryStallAfter,
		})
	}
	deps := controlapi.AppDeps{
		Persist:        persistStore,
		Queue:          persistQueue,
		AdvisoryLocker: cfg.Driver.AdvisoryLocker(),
		Clock:          cfg.Clock,
		Logger:         cfg.Logger,
		AuthState:      authState,
		ClaimProducers: registry,
		LifecycleSubs:  lifecycleReg,
		LifecycleKick:  lifecycleDrain.Kick,
		NamedLocks:     cfg.NamedLocks,
		Executors:      executorsByName,
		ExecutorCapabilities: func(executorName string) ([]string, []string, []byte, bool) {
			service, ok := disc.GetExecutor(executorName)
			if !ok || service.Capabilities == nil {
				return nil, nil, nil, false
			}
			return service.Capabilities.DeclaredTags,
				service.Capabilities.DeclaredErrorClasses,
				service.Capabilities.ExpectedAttributesSchema,
				true
		},
		ClaimProducerDeclaredErrorClasses: func(producerName string) ([]string, bool) {
			service, ok := disc.GetClaimProducer(producerName)
			if !ok || service.Capabilities == nil {
				return nil, false
			}
			return service.Capabilities.DeclaredErrorClasses, true
		},
		Observability: func(r chi.Router) {
			observability.Routes(r, observability.Deps{
				Tables:         persistStore,
				Queue:          persistQueue,
				Driver:         cfg.Driver,
				Executors:      execServices,
				ClaimProducers: storeServices,
				Discovery:      disc,
			})
		},
		Metrics:                    cfg.Metrics,
		Publishers:                 publisherReg,
		Validators:                 validationReg,
		UnreachableValidatorPolicy: runtime.UnreachableValidatorPolicy(cfg.UnreachableValidatorPolicy),
		DataProcessors:             dataProcessorReg,
		LateBindServiceProxies:     cfg.LateBindServiceProxies,
		KindAliases:                buildKindAliases(),
		ServiceAuth:                cfg.ServiceAuth,
		Enroll:                     enrollDeps,
	}
	app := controlapi.NewApp(deps)
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		stopIdentity()
		cancelDiscovery()
		registry.Close()
		lifecycleReg.Close()
		for _, c := range serviceClosers {
			c()
		}
		return nil, fmt.Errorf("StartControlAPI: listen: %w", err)
	}
	srv := &http.Server{Handler: app}
	// @decision: service-auth-mtls
	if tlsCfg := service.TLSControlAPIServerConfig(cfg.ServiceAuth, mtlsIdentity, caPoolOrNil(mtlsCA)); tlsCfg != nil {
		srv.TLSConfig = tlsCfg
		listener = tls.NewListener(listener, tlsCfg)
	}
	loopCtx, cancelLoops := context.WithCancel(context.Background())
	closersWithIdentity := append([]func(){stopIdentity}, serviceClosers...)
	h := &controlAPIHandle{
		srv:             srv,
		addr:            listener.Addr().String(),
		serveErr:        make(chan error, 1),
		registry:        registry,
		lifecycleReg:    lifecycleReg,
		cancelLoops:     cancelLoops,
		cancelDiscovery: cancelDiscovery,
		serviceClosers:  closersWithIdentity,
	}
	// @decision: lifecycle-drain-per-role
	if cfg.SharedLifecycleDrain == nil {
		h.lifecycleDrain = lifecycleDrain
		h.goWG(func() { lifecycleDrain.Run(loopCtx) })
	}
	h.goWG(func() { disc.RefreshLoop(discoveryCtx, cfg.ObservabilityRefreshInterval, obsLogger) })
	h.goWG(func() {
		defer close(h.serveErr)
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			if cfg.Logger != nil {
				cfg.Logger.Error("CONTROLAPI.SERVER.SERVEFAILED", "error", err.Error())
			}
			h.serveErr <- err
		}
	})
	h.goWG(func() { controlapi.WatchAnonymousMode(loopCtx, authState, controlapi.DefaultBannerInterval) })
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
	resyncPublishers := cfg.ResyncPublishers
	if resyncPublishers == nil {
		resyncPublishers = runtime.ResyncPublisherSubscriptions
	}
	reconcileSubscriptions := cfg.ReconcilePublisherSubscriptions
	if reconcileSubscriptions == nil {
		reconcileSubscriptions = runtime.RunPublisherSubscriptionReconciler
	}
	h.goWG(func() {
		if err := resyncPublishers(loopCtx, publisherDeps); err != nil {
			resyncLog.Warn("CONTROLAPI.PUBLISHERRESYNC.FAILED", "error", err.Error())
		}
	})
	h.goWG(func() {
		reconcileSubscriptions(loopCtx, publisherDeps,
			runtime.DefaultPublisherSubscriptionReconcileInterval)
	})
	return h, nil
}

func defaultControlAPIPrincipal() string {
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return fmt.Sprintf("control-api-%s-%d", hostname, os.Getpid())
	}
	return fmt.Sprintf("control-api-default-%d", os.Getpid())
}

// @concept: node
func buildKindAliases() *node.KindAliasMap {
	m := node.NewKindAliasMap()
	if err := builtin.RegisterAllKindAliases(m); err != nil {
		panic(fmt.Sprintf("controlapi: build kind aliases: %v", err))
	}
	return m
}

func mergeBundledExecutorEntries(configured map[string]controlapi.ExecutorEntry, bundled map[string]executor.Endpoint) {
	for name, ep := range bundled {
		if _, exists := configured[name]; exists {
			continue
		}
		configured[name] = controlapi.ExecutorEntry{
			Transport: ep.Transport,
			Endpoint:  ep.URL,
		}
	}
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

// @decision: service-auth-mtls
func caPoolOrNil(ca *pki.CA) *x509.CertPool {
	if ca == nil {
		return nil
	}
	return ca.CertPool()
}
