// rimsky-supervisor is the YAML-configured entry point for the
// supervisor process. Reads its config path from
// RIMSKY_SUPERVISOR_CONFIG, parses the YAML into a typed struct, wires
// dependencies (pgxpool, storage, queue, resolver), loads the stores
// config from RIMSKY_STORES_CONFIG (per spec §6.1: name → endpoint +
// declared capabilities), and calls config.StartSupervisor which dials
// each remote store-service.
//
// Environment variables:
//
//	RIMSKY_SUPERVISOR_CONFIG  required; path to the supervisor YAML.
//	RIMSKY_STORES_CONFIG      optional; path to stores.yml.
//	                          default /etc/rimsky/stores.yml.
//	RIMSKY_LOG_LEVEL          optional; debug|info|warn|error (default info).
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
	"github.com/fallguy/rimsky/core/executor"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// defaultStoresConfigPath is the path used when RIMSKY_STORES_CONFIG is
// unset.
const defaultStoresConfigPath = "/etc/rimsky/stores.yml"

type yamlConfig struct {
	PostgresURL         string                  `yaml:"postgres_url"`
	SupervisorID        string                  `yaml:"supervisor_id"`
	Concurrency         int                     `yaml:"concurrency"`
	HeartbeatIntervalMs int                     `yaml:"heartbeat_interval_ms"`
	ClaimPollIntervalMs int                     `yaml:"claim_poll_interval_ms"`
	Callback            yamlCallback            `yaml:"callback"`
	Executors           map[string]yamlExecutor `yaml:"executors"`
}

type yamlCallback struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	AdvertiseHost string `yaml:"advertise_host"`
	AdvertisePort int    `yaml:"advertise_port"`
}

type yamlExecutor struct {
	Transport string `yaml:"transport"`
	Endpoint  string `yaml:"endpoint"`
	TLS       string `yaml:"tls"`
}

func main() {
	cfgPath := os.Getenv("RIMSKY_SUPERVISOR_CONFIG")
	if cfgPath == "" {
		fmt.Fprintln(os.Stderr, "rimsky-supervisor: missing RIMSKY_SUPERVISOR_CONFIG (path to YAML)")
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("RIMSKY_LOG_LEVEL"))})))
	log := shared.NewSlogLogger(slog.Default())

	cfg, err := loadYAML(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		os.Exit(1)
	}

	storesPath := os.Getenv("RIMSKY_STORES_CONFIG")
	if storesPath == "" {
		storesPath = defaultStoresConfigPath
	}
	storesCfg, namedLocksCfg, err := config.LoadStoresConfigYAML(storesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		os.Exit(1)
	}

	dsn := cfg.PostgresURL
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "rimsky-supervisor: postgres_url required")
		os.Exit(1)
	}

	supID := cfg.SupervisorID
	if supID == "" {
		hostname, _ := os.Hostname()
		supID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	concurrency := cfg.Concurrency
	if concurrency < 1 {
		concurrency = 4
	}
	heartbeatMs := cfg.HeartbeatIntervalMs
	if heartbeatMs < 100 {
		heartbeatMs = 5000
	}
	claimPollMs := cfg.ClaimPollIntervalMs
	if claimPollMs < 50 {
		claimPollMs = 1000
	}

	endpoints := map[string]executor.Endpoint{}
	for name, e := range cfg.Executors {
		endpoints[name] = executor.Endpoint{
			Transport: e.Transport,
			URL:       e.Endpoint,
			TLS:       e.TLS,
		}
	}

	callbackHost := cfg.Callback.Host
	if callbackHost == "" {
		callbackHost = "0.0.0.0"
	}
	callbackPort := cfg.Callback.Port

	advertiseHost := os.Getenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST")
	if advertiseHost == "" {
		advertiseHost = cfg.Callback.AdvertiseHost
	}
	advertisePort := 0
	if s := os.Getenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			advertisePort = n
		} else {
			fmt.Fprintf(os.Stderr, "rimsky-supervisor: invalid RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT %q: %v\n", s, err)
			os.Exit(1)
		}
	}
	if advertisePort == 0 {
		advertisePort = cfg.Callback.AdvertisePort
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("pgxpool.New", "error", err.Error())
		os.Exit(1)
	}

	sb := pgstorage.New(pool)
	q := pgqueue.New(pool)

	resolver := executor.NewStaticResolver(endpoints)

	h, err := config.StartSupervisor(config.SupervisorConfig{
		SupervisorID:          supID,
		Storage:               sb,
		Queue:                 q,
		Clock:                 shared.SystemClock{},
		Logger:                log,
		Concurrency:           concurrency,
		HeartbeatInterval:     time.Duration(heartbeatMs) * time.Millisecond,
		ClaimPollInterval:     time.Duration(claimPollMs) * time.Millisecond,
		Resolver:              resolver,
		Stores:                storesCfg,
		NamedLocks:            namedLocksCfg,
		CallbackHost:          callbackHost,
		CallbackPort:          callbackPort,
		CallbackAdvertiseHost: advertiseHost,
		CallbackAdvertisePort: advertisePort,
	})
	if err != nil {
		log.Error("StartSupervisor", "error", err.Error())
		pool.Close()
		os.Exit(1)
	}
	log.Info("supervisor started", "id", supID, "callback_addr", h.CallbackAddr())

	waitForSignal(log)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		log.Error("supervisor shutdown", "error", err.Error())
	}
	pool.Close()
}

// loadYAML reads the supervisor YAML, expanding ${ENV_VAR} references.
func loadYAML(path string) (yamlConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return yamlConfig{}, fmt.Errorf("read config %q: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	var cfg yamlConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return yamlConfig{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg, nil
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
