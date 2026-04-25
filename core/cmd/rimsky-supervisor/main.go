// rimsky-supervisor is the reference YAML-configured entry point for the
// supervisor process. Reads a YAML config path from RIMSKY_SUPERVISOR_CONFIG,
// parses it into a typed struct, wires dependencies (pgxpool, storage, queue,
// resolver, inline-jsonb resource factory), and calls config.StartSupervisor.
// SIGTERM/SIGINT triggers a 30s graceful shutdown.
//
// Environment variables:
//
//	RIMSKY_SUPERVISOR_CONFIG  required; path to a YAML config file.
//	RIMSKY_LOG_LEVEL          optional; debug|info|warn|error (default info).
//
// YAML shape (values support ${ENV_VAR} expansion via os.ExpandEnv):
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
//	sql_connections: {}         # reserved for Plan C external-sql; Plan A leaves empty
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
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/resource/inlinejsonb"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// yamlConfig mirrors the YAML shape documented in the package doc.
type yamlConfig struct {
	PostgresURL         string                  `yaml:"postgres_url"`
	SupervisorID        string                  `yaml:"supervisor_id"`
	Concurrency         int                     `yaml:"concurrency"`
	HeartbeatIntervalMs int                     `yaml:"heartbeat_interval_ms"`
	ClaimPollIntervalMs int                     `yaml:"claim_poll_interval_ms"`
	Callback            yamlCallback            `yaml:"callback"`
	Executors           map[string]yamlExecutor `yaml:"executors"`
	SQLConnections      map[string]yamlSQLConn  `yaml:"sql_connections"`
	ConcurrencyLimits   map[string]int          `yaml:"concurrency_limits"`
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

type yamlSQLConn struct {
	URL string `yaml:"url"`
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

	// Plan A: inline-jsonb only, so sql_connections is typically empty.
	sqlConnections := map[string]*pgxpool.Pool{}
	for name, sc := range cfg.SQLConnections {
		if sc.URL == "" {
			continue
		}
		p, err := pgxpool.New(context.Background(), sc.URL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rimsky-supervisor: sql_connection %q: %v\n", name, err)
			os.Exit(1)
		}
		sqlConnections[name] = p
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("pgxpool.New", "error", err.Error())
		closePools(sqlConnections)
		os.Exit(1)
	}

	sb := pgstorage.New(pool)
	q := pgqueue.New(pool)

	// Per-process factory registry. Plan A only supports inline-jsonb;
	// external-sql is a Plan C concern.
	factories := resource.NewRegistry()
	factories.Register("inline-jsonb", inlinejsonb.Factory{StorageRegistry: sb.Resources()})

	resolver := executor.NewStaticResolver(endpoints)

	h, err := config.StartSupervisor(config.SupervisorConfig{
		SupervisorID:      supID,
		Storage:           sb,
		Queue:             q,
		Clock:             shared.SystemClock{},
		Logger:            log,
		Concurrency:       concurrency,
		HeartbeatInterval: time.Duration(heartbeatMs) * time.Millisecond,
		ClaimPollInterval: time.Duration(claimPollMs) * time.Millisecond,
		Resolver:          resolver,
		GetResource: func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error) {
			return resolveInlineJsonbResource(ctx, sb, factories, resourceID)
		},
		ResourceFactories:     factories,
		ConcurrencyLimits:     cfg.ConcurrencyLimits,
		SQLConnections:        sqlConnections,
		CallbackHost:          callbackHost,
		CallbackPort:          callbackPort,
		CallbackAdvertiseHost: advertiseHost,
		CallbackAdvertisePort: advertisePort,
	})
	if err != nil {
		log.Error("StartSupervisor", "error", err.Error())
		pool.Close()
		closePools(sqlConnections)
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
	closePools(sqlConnections)
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

// resolveInlineJsonbResource fetches the resource row by id and constructs a
// Resource via the supplied explicit factory registry.
func resolveInlineJsonbResource(ctx context.Context, sb storage.StorageBackend, factories *resource.FactoryRegistry, resourceID shared.UUID) (resource.Resource, error) {
	row, err := sb.Resources().Get(ctx, resourceID, nil)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	fac, ok := factories.Get("inline-jsonb")
	if !ok {
		return nil, fmt.Errorf("inline-jsonb factory not registered")
	}
	cfg := resource.Config{
		"_resource_id":   resourceID.String(),
		"_path":          row.ResourcePath,
		"_owner_node_id": row.OwnerNodeID.String(),
		"keep_versions":  row.KeepVersions,
	}
	return fac.Create(cfg, nil, nil)
}

func closePools(pools map[string]*pgxpool.Pool) {
	for _, p := range pools {
		p.Close()
	}
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
