package config

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/fallguy/rimsky/core/controlapi"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

type ControlAPIConfig struct {
	Storage storage.StorageBackend
	Queue   queue.DispatchQueue
	Clock   shared.Clock
	Logger  shared.Logger
	Host    string
	Port    int
	Auth    controlapi.Authenticator // nil = anonymous (default)
	// ResourceFactories is the explicit factory registry consulted by
	// template validation and instance provisioning. If nil,
	// resource.DefaultRegistry() is used — this preserves backward-compat
	// for callers still relying on resource.RegisterFactory.
	ResourceFactories *resource.FactoryRegistry
}

type ControlAPIHandle interface {
	Shutdown(ctx context.Context) error
	Addr() string
}

type controlAPIHandle struct {
	srv  *http.Server
	addr string
}

func (h *controlAPIHandle) Shutdown(ctx context.Context) error {
	if h.srv == nil {
		return nil
	}
	return h.srv.Shutdown(ctx)
}
func (h *controlAPIHandle) Addr() string { return h.addr }

// StartControlAPI binds host:port (port=0 for OS-assigned) and starts serving.
// Returns a handle whose Addr reports the bound address.
func StartControlAPI(cfg ControlAPIConfig) (ControlAPIHandle, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	factories := cfg.ResourceFactories
	if factories == nil {
		factories = resource.DefaultRegistry()
	}
	app := controlapi.NewApp(controlapi.AppDeps{
		Storage: cfg.Storage, Queue: cfg.Queue, Clock: cfg.Clock,
		Logger: cfg.Logger, Auth: cfg.Auth,
		ResourceFactories: factories,
	})
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, fmt.Errorf("StartControlAPI: listen: %w", err)
	}
	srv := &http.Server{Handler: app}
	h := &controlAPIHandle{srv: srv, addr: listener.Addr().String()}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed && cfg.Logger != nil {
			cfg.Logger.Error("controlapi serve", "error", err.Error())
		}
	}()
	return h, nil
}
