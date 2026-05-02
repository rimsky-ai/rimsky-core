package config

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/fallguy/rimsky/core/controlapi"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// ControlAPIConfig wires the control-api HTTP server. The store config
// follows the same name → endpoint + capabilities shape as the
// supervisor and scheduler. Per spec §6.1.
type ControlAPIConfig struct {
	Storage storage.StorageBackend
	Queue   queue.DispatchQueue
	Clock   shared.Clock
	Logger  shared.Logger
	Host    string
	Port    int
	Auth    controlapi.Authenticator // nil = anonymous (default)
	Stores  RemoteStoresConfig
	// NamedLocks is the operator-side named-lock config. The control-
	// api consults this at template-deploy time to validate that
	// every template-referenced lock name is declared.
	NamedLocks store.NamedLocksConfig
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
	srv         *http.Server
	addr        string
	registry    *store.Registry
	terminator  *controlapi.InstanceTerminator
	cancelLoops context.CancelFunc
}

func (h *controlAPIHandle) Shutdown(ctx context.Context) error {
	var err error
	if h.srv != nil {
		err = h.srv.Shutdown(ctx)
	}
	if h.cancelLoops != nil {
		h.cancelLoops()
	}
	// Close the store registry before waiting for the terminator: any
	// in-flight RPCs surface gRPC "connection closed" errors, the
	// terminator's tickBudget bounds it from outside, and Stop's
	// stopBudget bounds the join. This ordering prevents a wedged
	// store RPC from blocking process shutdown forever.
	if h.registry != nil {
		h.registry.Close()
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
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	registry, err := dialRemoteStores(context.Background(), cfg.Stores)
	if err != nil {
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		registry.Close()
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	if err := cfg.Executors.Validate(); err != nil {
		registry.Close()
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
	deps := controlapi.AppDeps{
		Storage: cfg.Storage, Queue: cfg.Queue, Clock: cfg.Clock,
		Logger: cfg.Logger, Auth: cfg.Auth,
		Stores:     registry,
		NamedLocks: cfg.NamedLocks,
		Executors:  executorsByName,
	}
	app := controlapi.NewApp(deps)
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		registry.Close()
		return nil, fmt.Errorf("StartControlAPI: listen: %w", err)
	}
	srv := &http.Server{Handler: app}
	terminator := controlapi.NewInstanceTerminator(deps, 0)
	loopCtx, cancelLoops := context.WithCancel(context.Background())
	h := &controlAPIHandle{
		srv:         srv,
		addr:        listener.Addr().String(),
		registry:    registry,
		terminator:  terminator,
		cancelLoops: cancelLoops,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed && cfg.Logger != nil {
			cfg.Logger.Error("controlapi serve", "error", err.Error())
		}
	}()
	go terminator.Run(loopCtx)
	return h, nil
}
