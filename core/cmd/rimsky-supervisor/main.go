// rimsky-supervisor is the reference YAML-configured entry point for the
// supervisor process. Reads a YAML config path from RIMSKY_SUPERVISOR_CONFIG,
// parses it into a typed struct, wires dependencies (pgxpool, storage, queue,
// resolver), loads the stores config from RIMSKY_STORES_CONFIG and registers
// the linked-in store factories (filesystem, claim-store-postgres), then
// calls config.StartSupervisor. SIGTERM/SIGINT triggers a 30s graceful
// shutdown.
//
// Environment variables:
//
//	RIMSKY_SUPERVISOR_CONFIG  required; path to the supervisor YAML config.
//	RIMSKY_STORES_CONFIG      optional; path to stores.yml.
//	                          default /etc/rimsky/stores.yml.
//	RIMSKY_LOG_LEVEL          optional; debug|info|warn|error (default info).
//
// Supervisor YAML shape (values support ${ENV_VAR} expansion via os.ExpandEnv):
//
//	postgres_url: "${RIMSKY_DB_URL}"
//	supervisor_id: ""           # optional; defaults to hostname-pid
//	concurrency: 4
//	heartbeat_interval_ms: 5000
//	claim_poll_interval_ms: 1000
//	callback:
//	  host: 0.0.0.0
//	  port: 0                   # 0 = OS-assigned
//	  advertise_host: ""        # optional; peer-reachable host (e.g. docker service name)
//	  advertise_port: 0         # optional; defaults to listener port when advertise_host set
//	executors:
//	  my-executor:
//	    transport: grpc         # grpc | http
//	    endpoint: "dns:///my-executor:50051"
//	    tls: off                # off | optional | required
//
// stores.yml shape (spec §14.1) is documented in
// docs/specs/2026-04-25-stores-redesign-design.md.
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
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/claimstorepg"
	"github.com/fallguy/rimsky/core/store/filesystem"
)

// defaultStoresConfigPath is the path used when RIMSKY_STORES_CONFIG is unset.
const defaultStoresConfigPath = "/etc/rimsky/stores.yml"

// yamlConfig mirrors the YAML shape documented in the package doc.
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
	storesCfg, err := loadStoresConfig(storesPath)
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

	// Advertised callback host:port preference order:
	//   1. RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_{HOST,PORT} env vars
	//   2. YAML `callback.advertise_host` / `callback.advertise_port`
	//   3. Unset → supervisor falls back to listener addr (see supervisor.go)
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

	// Linked-in store factories. The reference binary registers the two
	// production-ready store kinds (`filesystem` direct-mode and
	// `claim_store` postgres-backed). Embedding deployers can extend this
	// list with custom factories before calling config.StartSupervisor.
	storeFactories := []store.Factory{
		filesystem.Factory{},
		claimstorepg.Factory{Pool: pool},
	}

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
		StoreFactories:        storeFactories,
		Stores:                storesCfg,
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

// loadYAML reads the given path, expands ${ENV_VAR} references in every string
// field, and decodes into yamlConfig. Env expansion runs on the raw bytes
// before YAML parsing, so all string values see it uniformly.
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

// loadStoresConfig reads the stores.yml file, expanding ${ENV_VAR} references
// before YAML parsing. A missing file is not an error: an empty
// store.StoresConfig is returned, which produces an empty registry and lets
// supervisors run without any stores configured (useful for stub-only test
// stacks). Any other read or parse error is propagated.
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
