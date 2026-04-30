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
}

type ControlAPIHandle interface {
	Shutdown(ctx context.Context) error
	Addr() string
}

type controlAPIHandle struct {
	srv      *http.Server
	addr     string
	registry *store.Registry
}

func (h *controlAPIHandle) Shutdown(ctx context.Context) error {
	var err error
	if h.srv != nil {
		err = h.srv.Shutdown(ctx)
	}
	if h.registry != nil {
		h.registry.Close()
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
	app := controlapi.NewApp(controlapi.AppDeps{
		Storage: cfg.Storage, Queue: cfg.Queue, Clock: cfg.Clock,
		Logger: cfg.Logger, Auth: cfg.Auth,
		Stores:     registry,
		NamedLocks: cfg.NamedLocks,
	})
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		registry.Close()
		return nil, fmt.Errorf("StartControlAPI: listen: %w", err)
	}
	srv := &http.Server{Handler: app}
	h := &controlAPIHandle{srv: srv, addr: listener.Addr().String(), registry: registry}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed && cfg.Logger != nil {
			cfg.Logger.Error("controlapi serve", "error", err.Error())
		}
	}()
	return h, nil
}
