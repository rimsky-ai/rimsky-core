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

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/controlapi"
	"github.com/fallguy/rimsky/modeling/observability"
	"github.com/fallguy/rimsky/modeling/shared"
)

// ControlAPIConfig wires the control-api HTTP server. The store config
// follows the same name → endpoint + capabilities shape as the
// supervisor and scheduler. Per spec §6.1.
type ControlAPIConfig struct {
	// Driver is the unified persistence driver. Required.
	Driver persistence.Driver
	Clock  shared.Clock
	Logger shared.Logger
	Host   string
	Port   int
	Auth   controlapi.Authenticator // nil = anonymous (default)
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
	persistStore := cfg.Driver.Store()
	if persistStore == nil {
		return nil, fmt.Errorf("StartControlAPI: Driver.Store() returned nil — driver did not initialize the Store accessor")
	}
	persistQueue := cfg.Driver.Queue()
	if persistQueue == nil {
		return nil, fmt.Errorf("StartControlAPI: Driver.Queue() returned nil")
	}
	registry, err := dialRemoteStores(context.Background(), cfg.Stores)
	if err != nil {
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	lifecycleReg, err := dialLifecycleSubscribers(context.Background(), cfg.Stores, cfg.Executors)
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
	go disc.RefreshLoop(discoveryCtx, observabilityRefreshInterval(), obsLogger)
	deps := controlapi.AppDeps{
		Persist:       persistStore,
		Queue:         persistQueue,
		Clock:         cfg.Clock,
		Logger:        cfg.Logger,
		Auth:          cfg.Auth,
		Stores:        registry,
		LifecycleSubs: lifecycleReg,
		NamedLocks:    cfg.NamedLocks,
		Executors:     executorsByName,
		Observability: func(r chi.Router) {
			observability.Routes(r, observability.Deps{
				Store:     persistStore,
				Queue:     persistQueue,
				Driver:    cfg.Driver,
				Executors: execPeers,
				Stores:    storePeers,
				Discovery: disc,
			})
		},
	}
	app := controlapi.NewApp(deps)
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		cancelDiscovery()
		registry.Close()
		lifecycleReg.Close()
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
	}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed && cfg.Logger != nil {
			cfg.Logger.Error("controlapi serve", "error", err.Error())
		}
	}()
	go terminator.Run(loopCtx)
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

// observabilityRefreshInterval returns the configured background re-
// probe interval, parsed from RIMSKY_OBSERVABILITY_REFRESH_INTERVAL
// (Go time.Duration syntax). Defaults to 60s per spec §4.
func observabilityRefreshInterval() time.Duration {
	if v := os.Getenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 60 * time.Second
}
