// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package launch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

// supervisorYAMLConfig is the supervisor-tuning YAML loaded from
// RIMSKY_SUPERVISOR_CONFIG. Persistence config lives in rimsky.yml under
// RIMSKY_CONFIG, not here.
type supervisorYAMLConfig struct {
	SupervisorID        string                 `yaml:"supervisor_id"`
	Concurrency         int                    `yaml:"concurrency"`
	HeartbeatIntervalMs int                    `yaml:"heartbeat_interval_ms"`
	ClaimPollIntervalMs int                    `yaml:"claim_poll_interval_ms"`
	Callback            supervisorYAMLCallback `yaml:"callback"`
}

type supervisorYAMLCallback struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	AdvertiseHost string `yaml:"advertise_host"`
	AdvertisePort int    `yaml:"advertise_port"`
}

// RunSupervisor wires and starts the supervisor role: supervisor-tuning
// YAML load (RIMSKY_SUPERVISOR_CONFIG), rimsky.yml load, persistence
// open, blob-backend install, the executor observability handshake,
// config.StartSupervisor, the metrics gauge refresher, the capability
// refresh loop, and the optional /metrics listener. Errors are logged
// on the supplied logger / written to stderr (matching the standalone
// binary's output) and returned. The returned StopFunc shuts the role
// down.
//
// Environment variables (identical to the rimsky-supervisor binary):
//
//	RIMSKY_SUPERVISOR_CONFIG  required; path to the supervisor YAML.
//	RIMSKY_CONFIG             optional; default /etc/rimsky/rimsky.yml.
//	RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST / _PORT  optional overrides.
//	RIMSKY_METRICS_PORT       optional; default 0 = disabled (see
//	                          metricsPortFor: per-role override via
//	                          RIMSKY_METRICS_PORT_SUPERVISOR; offset
//	                          base+1 in unified mode).
//	RIMSKY_METRICS_HOST       optional; default 127.0.0.1.
func RunSupervisor(ctx context.Context, logger *slog.Logger) (StopFunc, <-chan error, error) {
	cfgPath := os.Getenv("RIMSKY_SUPERVISOR_CONFIG")
	if cfgPath == "" {
		err := fmt.Errorf("missing RIMSKY_SUPERVISOR_CONFIG (path to YAML)")
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		return nil, nil, err
	}
	log := shared.NewSlogLogger(logger)

	// Resolve the /metrics port up-front so a malformed env value fails
	// the role at startup instead of silently disabling metrics.
	metricsPort, err := metricsPortFor("supervisor")
	if err != nil {
		log.Error("metrics port resolution", "error", err.Error())
		return nil, nil, err
	}

	cfg, err := loadSupervisorYAML(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		return nil, nil, err
	}

	configPath := os.Getenv("RIMSKY_CONFIG")
	if configPath == "" {
		configPath = defaultRimskyConfigPath
	}
	rimskyCfg, err := config.LoadRimskyConfigYAML(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		return nil, nil, err
	}
	if err := rimskyCfg.Executors.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		return nil, nil, err
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
	advertiseHostSource := "env:RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST"
	if advertiseHost == "" {
		advertiseHost = cfg.Callback.AdvertiseHost
		advertiseHostSource = "yaml:callback.advertise_host"
	}
	if advertiseHost == "" {
		advertiseHostSource = "unset"
	}
	advertisePort := 0
	if s := os.Getenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			advertisePort = n
		} else {
			err = fmt.Errorf("invalid RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT %q: %w", s, err)
			fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
			return nil, nil, err
		}
	}
	if advertisePort == 0 {
		advertisePort = cfg.Callback.AdvertisePort
	}

	// Surface the resolved callback advertise host (and where it came from)
	// at startup. Executors dial this address to POST async-callback
	// outcomes; when it is empty or a loopback, the POST fails with a bare
	// "fetch failed" and no other diagnostic — a silent misconfiguration in
	// any multi-container deployment. The bind host (callbackHost, typically
	// 0.0.0.0 in a container) is NOT how executors reach the supervisor; only
	// the advertise host is, so warn rather than log quietly when it can't be
	// reached from another container.
	if advertiseHost == "" || advertiseHost == "127.0.0.1" || advertiseHost == "localhost" || advertiseHost == "::1" {
		log.Warn("callback advertise host is loopback or unset — executors on other hosts/containers cannot reach this supervisor",
			"advertise_host", advertiseHost,
			"source", advertiseHostSource,
			"bind_host", callbackHost,
			"bind_port", callbackPort,
			"hint", "set RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST (or callback.advertise_host) to a hostname executors can reach this supervisor at")
	} else {
		log.Info("callback advertise host resolved",
			"advertise_host", advertiseHost,
			"source", advertiseHostSource,
			"advertise_port", advertisePort)
	}

	driver, err := persistence.Open(ctx, rimskyCfg.Persistence)
	if err != nil {
		log.Error("persistence.Open", "error", err.Error())
		return nil, nil, err
	}

	// Construct the BlobBackend selected by rimsky.yml's persistence.blob
	// block and install it on the driver. The attribute write/read path
	// consults the driver-installed backend directly; the named-event /
	// parked-payload write paths receive it via SupervisorConfig.Blob
	// (threaded through to RunArgs).
	blobBackend, err := config.OpenBlobBackend(rimskyCfg.Blob, driver)
	if err != nil {
		log.Error("config.OpenBlobBackend", "error", err.Error())
		_ = driver.Close()
		return nil, nil, err
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
	disc := observability.RunHandshake(ctx, observability.NewGRPCProber(), execPeers, nil, logger)

	// Per-role Prometheus registry. Constructed up-front so the
	// supervisor's integration runtime can be instrumented via the
	// MetricsHook adapter; the /metrics HTTP listener is opened below
	// only when RIMSKY_METRICS_PORT > 0.
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
		return nil, nil, err
	}
	log.Info("supervisor started", "id", supID, "callback_addr", h.CallbackAddr())

	// Launch the gauge refresher so node-state, parked-by-reason,
	// held-frames, and dispatch-queue-depth gauges reflect live
	// persistence state. The refresher polls every 5s by default;
	// cancelled on stop.
	gaugeCtx, cancelGauges := context.WithCancel(context.Background())
	if mhook := observability.MetricsHookOf(mreg); mhook != nil {
		mhook.StartGaugeRefresher(gaugeCtx, driver.Tables(), driver.Queue(), 0, log)
	}

	// Refresh the executor capability cache periodically so the
	// expected-attributes-schema resolver sees healed peers without a
	// process restart.
	go disc.RefreshLoop(gaugeCtx, config.ObservabilityRefreshInterval(), logger)

	// Capacity 2: the metrics serve loop and the callback serve loop can
	// each report one failure.
	reporter := newFailureReporter(2)
	metricsSrv := startMetricsServer(metricsHostFromEnv(), "supervisor", metricsPort, mreg, log, reporter)

	// Surface a fatal post-start death of the async-callback HTTP serve
	// loop as a role failure. Without this the supervisor runs degraded
	// forever: executors' async callbacks black-hole while the claim
	// loop keeps dispatching. The handle's channel closes when the serve
	// loop exits (clean shutdown sends nothing), so this monitor exits
	// on stop rather than leaking.
	go func() {
		if err, ok := <-h.CallbackServeErr(); ok && err != nil {
			reporter.Report(fmt.Errorf("supervisor callback endpoint: %w", err))
		}
	}()

	stop := func(stopCtx context.Context) error {
		var firstErr error
		// stopCtx's deadline is shared across both servers: the supervisor
		// handle's shutdown and the metrics server's drain below.
		if err := h.Shutdown(stopCtx); err != nil {
			log.Error("supervisor shutdown", "error", err.Error())
			firstErr = err
		}
		if metricsSrv != nil {
			if err := metricsSrv.Shutdown(stopCtx); err != nil {
				log.Warn("metrics server shutdown", "error", err.Error())
			}
		}
		cancelGauges()
		_ = driver.Close()
		// Close the fail channel so monitor goroutines reading it exit;
		// RunSupervisor is embeddable and must not leak a monitor per
		// start/stop cycle.
		reporter.Close()
		return firstErr
	}
	return stop, reporter.ch, nil
}

// loadSupervisorYAML reads the supervisor YAML, expanding ${ENV_VAR}
// references.
func loadSupervisorYAML(path string) (supervisorYAMLConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return supervisorYAMLConfig{}, fmt.Errorf("read config %q: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	var cfg supervisorYAMLConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return supervisorYAMLConfig{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg, nil
}
