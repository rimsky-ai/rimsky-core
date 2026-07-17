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

type supervisorYAMLConfig struct {
	SupervisorID        string                 `yaml:"supervisor_id"`
	Concurrency         int                    `yaml:"concurrency"`
	LivenessIntervalMs  int                    `yaml:"liveness_interval_ms"`
	ClaimPollIntervalMs int                    `yaml:"claim_poll_interval_ms"`
	Callback            supervisorYAMLCallback `yaml:"callback"`
}

type supervisorYAMLCallback struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	AdvertiseHost string `yaml:"advertise_host"`
	AdvertisePort int    `yaml:"advertise_port"`
}

func RunSupervisor(ctx context.Context, logger *slog.Logger, driver persistence.Database, rimskyCfg *config.RimskyConfig, bundledRegs *config.BundledRegistrations) (StopFunc, <-chan error, error) {
	cfgPath := os.Getenv("RIMSKY_SUPERVISOR_CONFIG")
	if cfgPath == "" {
		err := fmt.Errorf("missing RIMSKY_SUPERVISOR_CONFIG (path to YAML)")
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		return nil, nil, err
	}
	log := shared.NewSlogLogger(logger)

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

	if err := rimskyCfg.Executors.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-supervisor: %v\n", err)
		return nil, nil, err
	}
	storesCfg := rimskyCfg.ClaimProducers
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
	livenessMs := cfg.LivenessIntervalMs
	if livenessMs < 100 {
		livenessMs = 5000
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

	blobBackend, err := config.OpenBlobBackend(rimskyCfg.Blob, driver)
	if err != nil {
		log.Error("config.OpenBlobBackend", "error", err.Error())
		return nil, nil, err
	}

	resolver := executor.NewStaticResolver(endpoints)

	execPeers := make([]observability.PeerSpec, 0, len(rimskyCfg.Executors.Executors))
	for name, e := range rimskyCfg.Executors.Executors {
		execPeers = append(execPeers, observability.PeerSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		})
	}
	disc := observability.RunHandshake(ctx, observability.NewGRPCProber(), execPeers, nil, logger)

	if bundledRegs != nil {
		for name, ep := range bundledRegs.ExecutorAliases {
			if _, exists := endpoints[name]; exists {
				log.Info("bundled executor overridden by configured endpoint", "executor", name)
				continue
			}
			resolver.Register(name, ep)
		}
		bundledRegs.AdvertiseInto(disc)
	}

	mreg := observability.NewMetricsRegistry()

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
		LivenessInterval:            time.Duration(livenessMs) * time.Millisecond,
		ClaimPollInterval:           time.Duration(claimPollMs) * time.Millisecond,
		Resolver:                    resolver,
		ClaimProducers:              storesCfg,
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
		Bundled:                     bundledRegs,
		PeerAuth:                    rimskyCfg.PeerAuth,
	})
	if err != nil {
		log.Error("StartSupervisor", "error", err.Error())
		return nil, nil, err
	}
	log.Info("supervisor started", "id", supID, "callback_addr", h.CallbackAddr())

	gaugeCtx, cancelGauges := context.WithCancel(context.Background())
	if mhook := observability.MetricsHookOf(mreg); mhook != nil {
		mhook.StartGaugeRefresher(gaugeCtx, driver.Tables(), driver.Queue(), 0, log)
	}

	go disc.RefreshLoop(gaugeCtx, config.ObservabilityRefreshInterval(), logger)

	reporter := newFailureReporter(2)
	metricsSrv := startMetricsServer(metricsHostFromEnv(), "supervisor", metricsPort, mreg, log, reporter)

	go func() {
		if err, ok := <-h.CallbackServeErr(); ok && err != nil {
			reporter.Report(fmt.Errorf("supervisor callback endpoint: %w", err))
		}
	}()

	stop := func(stopCtx context.Context) error {
		var firstErr error
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
		reporter.Close()
		return firstErr
	}
	return stop, reporter.ch, nil
}

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
