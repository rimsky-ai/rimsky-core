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

// ControlAPIConfig wires the control-api HTTP server. The store wiring follows
// the same shape as SupervisorConfig (spec §16.2): the deployer registers a
// list of factories it has linked in and supplies the parsed `stores.yml`;
// StartControlAPI builds the per-process *store.Registry from the pair and
// hands it to the controlapi app.
type ControlAPIConfig struct {
	Storage storage.StorageBackend
	Queue   queue.DispatchQueue
	Clock   shared.Clock
	Logger  shared.Logger
	Host    string
	Port    int
	Auth    controlapi.Authenticator // nil = anonymous (default)
	// StoreFactories enumerates the store-kind factories registered with
	// this process. The deployer's main() builds this list from the set of
	// store implementations it has linked in (filesystem, claim-store-pg,
	// stub, custom). Required when Stores is non-empty.
	StoreFactories []store.Factory
	// Stores is the parsed YAML stores config (spec §14.1). Each entry is
	// keyed by operator-chosen store name; the value's "kind" picks a
	// factory from StoreFactories.
	Stores store.StoresConfig
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
	registry, err := buildStoreRegistry(cfg.StoreFactories, cfg.Stores)
	if err != nil {
		return nil, fmt.Errorf("StartControlAPI: %w", err)
	}
	app := controlapi.NewApp(controlapi.AppDeps{
		Storage: cfg.Storage, Queue: cfg.Queue, Clock: cfg.Clock,
		Logger: cfg.Logger, Auth: cfg.Auth,
		Stores: registry,
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
