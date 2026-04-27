// rimsky-control-api is the reference env-var-driven entry point for the
// control API HTTP server. Reads RIMSKY_DB_URL, RIMSKY_CONTROL_API_HOST, and
// RIMSKY_CONTROL_API_PORT, loads the stores config from RIMSKY_STORES_CONFIG,
// constructs a config.ControlAPIConfig, and calls config.StartControlAPI.
// SIGTERM/SIGINT triggers a 30s graceful shutdown.
//
// Environment variables:
//
//	RIMSKY_DB_URL            required; Postgres DSN.
//	RIMSKY_CONTROL_API_HOST  optional; default 127.0.0.1.
//	RIMSKY_CONTROL_API_PORT  optional; default 8080 (0 = OS-assigned).
//	RIMSKY_STORES_CONFIG     optional; path to stores.yml.
//	                         default /etc/rimsky/stores.yml.
//	RIMSKY_LOG_LEVEL         optional; debug|info|warn|error (default info).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/fallguy/rimsky/core/config"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/claimstorepg"
	"github.com/fallguy/rimsky/core/store/filesystem"
)

// defaultStoresConfigPath is the path used when RIMSKY_STORES_CONFIG is unset.
const defaultStoresConfigPath = "/etc/rimsky/stores.yml"

func main() {
	dsn := os.Getenv("RIMSKY_DB_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "rimsky-control-api: missing RIMSKY_DB_URL")
		os.Exit(1)
	}
	host := os.Getenv("RIMSKY_CONTROL_API_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port, _ := strconv.Atoi(os.Getenv("RIMSKY_CONTROL_API_PORT"))
	if port == 0 {
		port = 8080
	}
	logLevel := os.Getenv("RIMSKY_LOG_LEVEL")

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(logLevel)})))
	log := shared.NewSlogLogger(slog.Default())

	storesPath := os.Getenv("RIMSKY_STORES_CONFIG")
	if storesPath == "" {
		storesPath = defaultStoresConfigPath
	}
	storesCfg, err := loadStoresConfig(storesPath)
	if err != nil {
		log.Error("load stores config", "error", err.Error(), "path", storesPath)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("pgxpool.New", "error", err.Error())
		os.Exit(1)
	}

	sb := pgstorage.New(pool)
	q := pgqueue.New(pool)

	// Linked-in store factories. Mirrors rimsky-supervisor — the control-api
	// must register the same set so admin endpoints can resolve a store
	// reference to the same concrete instance.
	storeFactories := []store.Factory{
		filesystem.Factory{},
		claimstorepg.Factory{Pool: pool},
	}

	h, err := config.StartControlAPI(config.ControlAPIConfig{
		Storage:        sb,
		Queue:          q,
		Clock:          shared.SystemClock{},
		Logger:         log,
		Host:           host,
		Port:           port,
		StoreFactories: storeFactories,
		Stores:         storesCfg,
	})
	if err != nil {
		log.Error("StartControlAPI", "error", err.Error())
		pool.Close()
		os.Exit(1)
	}
	log.Info("control api listening", "addr", h.Addr())

	waitForSignal(log)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		log.Error("control api shutdown", "error", err.Error())
	}
	pool.Close()
}

// loadStoresConfig reads the stores.yml file, expanding ${ENV_VAR} references
// before YAML parsing. A missing file is not an error: an empty
// store.StoresConfig is returned, mirroring the supervisor binary's behaviour
// so a control-api can run alongside a stub-only test stack.
func loadStoresConfig(path string) (store.StoresConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store.StoresConfig{}, nil
		}
		return store.StoresConfig{}, fmt.Errorf("read stores config %q: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	var wrapper struct {
		Stores map[string]map[string]any `yaml:"stores"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err != nil {
		return store.StoresConfig{}, fmt.Errorf("parse stores config %q: %w", path, err)
	}
	return store.StoresConfig{Stores: wrapper.Stores}, nil
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func waitForSignal(log shared.Logger) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	s := <-sigCh
	log.Info("signal received", "signal", s.String())
}
