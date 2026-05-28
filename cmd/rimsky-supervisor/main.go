// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-supervisor is the YAML-configured entry point for the
// supervisor process. Reads its config path from
// RIMSKY_SUPERVISOR_CONFIG (per-process tuning: concurrency, callback,
// heartbeat), parses the YAML into a typed struct, wires dependencies
// (pgxpool, storage, queue, resolver), loads the unified deployment-
// shape config from RIMSKY_CONFIG (stores + named_locks + executors
// per docs/specs/2026-05-01-control-plane-and-store-lifecycle-
// design.md §3.1), and calls config.StartSupervisor which dials each
// remote store-service.
//
// Environment variables:
//
//	RIMSKY_SUPERVISOR_CONFIG  required; path to the supervisor YAML.
//	RIMSKY_CONFIG             optional; path to rimsky.yml.
//	                          default /etc/rimsky/rimsky.yml.
//	RIMSKY_METRICS_PORT       optional; default 0 = disabled. When >0
//	                          exposes /metrics on this port (Prometheus
//	                          text format) bound to RIMSKY_METRICS_HOST
//	                          (default 127.0.0.1).
//	RIMSKY_METRICS_HOST       optional; default 127.0.0.1.
//	RIMSKY_LOG_LEVEL          optional; debug|info|warn|error (default info).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres" // register driver
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"   // register driver
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

// defaultRimskyConfigPath is the path used when RIMSKY_CONFIG is unset.
const defaultRimskyConfigPath = "/etc/rimsky/rimsky.yml"

// yamlConfig is the supervisor-tuning YAML loaded from
// RIMSKY_SUPERVISOR_CONFIG. Persistence config lives in rimsky.yml under
// RIMSKY_CONFIG, not here.
type yamlConfig struct {
	SupervisorID        string       `yaml:"supervisor_id"`
	Concurrency         int          `yaml:"concurrency"`
	HeartbeatIntervalMs int          `yaml:"heartbeat_interval_ms"`
	ClaimPollIntervalMs int          `yaml:"claim_poll_interval_ms"`
	Callback            yamlCallback `yaml:"callback"`
}

type yamlCallback struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	AdvertiseHost string `yaml:"advertise_host"`
	AdvertisePort int    `yaml:"advertise_port"`
}

func main() {
	cfgPath := os.Getenv("RIMSKY_SUPERVISOR_CONFIG")
	if cfgPath == "" {
		fmt.Fprintln(os.Stderr, "rimsky-supervisor: missing RIMSKY_SUPERVISOR_CONFIG (path to YAML)")
		os.Exit(1)
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("RIMSKY_LOG_LEVEL"))})
	logger := slog.New(handler)
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		logger = logger.With("binary", name)
	}
	slog.SetDefault(logger)
	log := shared.NewSlogLogger(logger)

	cfg, err := loadYAML(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		os.Exit(1)
	}

	configPath := os.Getenv("RIMSKY_CONFIG")
	if configPath == "" {
		configPath = defaultRimskyConfigPath
	}
	rimskyCfg, err := config.LoadRimskyConfigYAML(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		os.Exit(1)
	}
	if err := rimskyCfg.Executors.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		os.Exit(1)
	}
	storesCfg := rimskyCfg.Stores
	namedLocksCfg := rimskyCfg.NamedLocks

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
	for name, e := range rimskyCfg.Executors.Executors {
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
	driver, err := persistence.Open(ctx, rimskyCfg.Persistence)
	if err != nil {
		log.Error("persistence.Open", "error", err.Error())
		os.Exit(1)
	}

	// Construct the BlobBackend selected by rimsky.yml's persistence.blob
	// block and install it on the driver. Plan §D0/D6/D7: the attribute
	// write/read path consults the driver-installed backend directly;
	// the named-event / parked-payload write paths receive it via
	// SupervisorConfig.Blob (threaded through to RunArgs).
	blobBackend, err := config.OpenBlobBackend(rimskyCfg.Blob, driver)
	if err != nil {
		log.Error("config.OpenBlobBackend", "error", err.Error())
		_ = driver.Close()
		os.Exit(1)
	}

	resolver := executor.NewStaticResolver(endpoints)

	// Run the observability handshake against each declared executor so
	// the dispatch-time effective-attribute-schema computation can see
	// the advertised expected_attributes_schema. The resolver closure
	// is plumbed into SupervisorConfig.ExpectedAttributesSchemaFor
	// below.
	execPeers := make([]observability.PeerSpec, 0, len(rimskyCfg.Executors.Executors))
	for name, e := range rimskyCfg.Executors.Executors {
		execPeers = append(execPeers, observability.PeerSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		})
	}
	disc := observability.RunHandshake(ctx, observability.NewGRPCProber(), execPeers, nil, slog.Default())

	// Plan I1/I2: per-process Prometheus registry. Constructed up-front
	// so the supervisor's integration runtime can be instrumented via
	// the MetricsHook adapter; the /metrics HTTP listener is opened
	// below only when RIMSKY_METRICS_PORT > 0.
	mreg := observability.NewMetricsRegistry()

	// Closure that invokes controlapi.LifecyclePeersForSpec with the
	// rimsky.yml late_bind_service_proxies baked in. Lives here (control/
	// layer) so the supervisor's runtime/ never imports control/ — the
	// late-bind-aware peer set crosses the layer boundary as a function
	// pointer (denied otherwise by .golangci.yml's runtime-purity rule).
	// Per spec 2026-05-24-host-agent-and-proxy-design.md.
	lateBindProxies := rimskyCfg.LateBindServiceProxies
	peersForSpec := func(tplSpec node.TemplateSpec) []string {
		return controlapi.LifecyclePeersForSpec(
			controlapi.AppDeps{LateBindServiceProxies: lateBindProxies},
			tplSpec,
		)
	}

	h, err := config.StartSupervisor(config.SupervisorConfig{
		SupervisorID:                supID,
		Driver:                      driver,
		Clock:                       shared.SystemClock{},
		Logger:                      log,
		Concurrency:                 concurrency,
		HeartbeatInterval:           time.Duration(heartbeatMs) * time.Millisecond,
		ClaimPollInterval:           time.Duration(claimPollMs) * time.Millisecond,
		Resolver:                    resolver,
		Stores:                      storesCfg,
		NamedLocks:                  namedLocksCfg,
		CallbackHost:                callbackHost,
		CallbackPort:                callbackPort,
		CallbackAdvertiseHost:       advertiseHost,
		CallbackAdvertisePort:       advertisePort,
		Blob:                        blobBackend,
		BlobSpillThreshold:          rimskyCfg.Blob.SpillThresholdBytes,
		ExpectedAttributesSchemaFor: observability.NewExpectedAttributesSchemaResolver(disc),
		Metrics:                     observability.MetricsHookOf(mreg),
		Executors:                   rimskyCfg.Executors,
		LifecyclePeersForSpec:       peersForSpec,
		LateBindServiceProxies:      lateBindProxies,
	})
	if err != nil {
		log.Error("StartSupervisor", "error", err.Error())
		_ = driver.Close()
		os.Exit(1)
	}
	log.Info("supervisor started", "id", supID, "callback_addr", h.CallbackAddr())

	// Plan I2: launch the gauge refresher so node-state, parked-by-reason,
	// held-frames, and dispatch-queue-depth gauges reflect live persistence
	// state. The refresher polls every 5s by default; cancel on shutdown.
	gaugeCtx, cancelGauges := context.WithCancel(context.Background())
	defer cancelGauges()
	if mhook := observability.MetricsHookOf(mreg); mhook != nil {
		mhook.StartGaugeRefresher(gaugeCtx, driver.Tables(), driver.Queue(), 0, log)
	}

	// Plan F6/F7: refresh the executor capability cache periodically so
	// the expected-attributes-schema resolver sees healed peers without
	// a process restart.
	go disc.RefreshLoop(gaugeCtx, config.ObservabilityRefreshInterval(), slog.Default())

	metricsHost := os.Getenv("RIMSKY_METRICS_HOST")
	if metricsHost == "" {
		metricsHost = "127.0.0.1"
	}
	metricsPort, _ := strconv.Atoi(os.Getenv("RIMSKY_METRICS_PORT"))
	var metricsSrv *http.Server
	if metricsPort > 0 {
		metricsRouter := chi.NewRouter()
		observability.MountMetrics(metricsRouter, mreg)
		metricsSrv = &http.Server{
			Addr:              fmt.Sprintf("%s:%d", metricsHost, metricsPort),
			Handler:           metricsRouter,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Info("metrics endpoint listening", "addr", metricsSrv.Addr)
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("metrics endpoint", "error", err.Error())
			}
		}()
	}

	waitForSignal(log)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		log.Error("supervisor shutdown", "error", err.Error())
	}
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
	_ = driver.Close()
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
