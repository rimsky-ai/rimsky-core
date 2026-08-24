// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/ports"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

type supervisorResolvedConfig struct {
	SupervisorID        string
	Concurrency         int
	LivenessInterval    time.Duration
	ClaimPollInterval   time.Duration
	CallbackHost        string
	CallbackPort        int
	AdvertiseHost       string
	AdvertiseHostSource string
	AdvertisePort       int
}

// @concept: rimsky-yml
// @decision: launch-config-injection
func resolveSupervisorConfig(cfg config.SupervisorSection) (supervisorResolvedConfig, error) {
	supID := cfg.SupervisorID
	if supID == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			supID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
		} else {
			supID = fmt.Sprintf("supervisor-default-%d", os.Getpid())
		}
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

	callbackHost := cfg.Callback.Host
	if callbackHost == "" {
		callbackHost = "0.0.0.0"
	}
	// @decision: default-port-allocation
	callbackPort := ports.SupervisorCallback
	if cfg.Callback.Port != nil {
		callbackPort = *cfg.Callback.Port
	}

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
		n, err := strconv.Atoi(s)
		if err != nil {
			return supervisorResolvedConfig{}, fmt.Errorf("invalid RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT %q: %w", s, err)
		}
		advertisePort = n
	}
	if advertisePort == 0 {
		advertisePort = cfg.Callback.AdvertisePort
	}

	return supervisorResolvedConfig{
		SupervisorID:        supID,
		Concurrency:         concurrency,
		LivenessInterval:    time.Duration(livenessMs) * time.Millisecond,
		ClaimPollInterval:   time.Duration(claimPollMs) * time.Millisecond,
		CallbackHost:        callbackHost,
		CallbackPort:        callbackPort,
		AdvertiseHost:       advertiseHost,
		AdvertiseHostSource: advertiseHostSource,
		AdvertisePort:       advertisePort,
	}, nil
}

func RunSupervisor(ctx context.Context, logger *slog.Logger, driver persistence.Database, rimskyCfg *config.RimskyConfig, opts RoleOptions) (StopFunc, <-chan error, error) {
	bundledRegs := opts.Bundled
	log := shared.NewSlogLogger(logger)

	metricsPort, err := metricsPortFor("supervisor", rimskyCfg.Topology)
	if err != nil {
		log.Error("METRICS.PORT.RESOLVEFAILED", "error", err.Error())
		return nil, nil, err
	}

	if err := rimskyCfg.Executors.Validate(); err != nil {
		log.Error("SUPERVISOR.EXECUTORSCONFIG.INVALID", "error", err.Error())
		return nil, nil, err
	}
	storesCfg := rimskyCfg.ClaimProducers
	namedLocksCfg := rimskyCfg.NamedLocks

	// @concept: rimsky-yml
	resolved, err := resolveSupervisorConfig(rimskyCfg.Supervisor)
	if err != nil {
		log.Error("SUPERVISOR.CONFIG.RESOLVEFAILED", "site", "resolveSupervisorConfig", "error", err.Error())
		return nil, nil, err
	}
	supID := resolved.SupervisorID
	concurrency := resolved.Concurrency

	configuredExecutors := map[string]executor.Endpoint{}
	for name, e := range rimskyCfg.Executors.Executors {
		configuredExecutors[name] = executor.Endpoint{
			Transport: e.Transport,
			URL:       e.Endpoint,
			TLS:       e.TLS,
		}
	}

	callbackHost := resolved.CallbackHost
	callbackPort := resolved.CallbackPort
	advertiseHost := resolved.AdvertiseHost
	advertiseHostSource := resolved.AdvertiseHostSource
	advertisePort := resolved.AdvertisePort

	if advertiseHost == "" || advertiseHost == "127.0.0.1" || advertiseHost == "localhost" || advertiseHost == "::1" {
		log.Warn("SUPERVISOR.CALLBACKADVERTISEHOST.UNREACHABLE", "detail", "the advertise host is loopback or unset, so executors on other hosts or containers cannot reach this supervisor",
			"advertise_host", advertiseHost,
			"source", advertiseHostSource,
			"bind_host", callbackHost,
			"bind_port", callbackPort,
			"hint", "set RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST (or callback.advertise_host) to a hostname executors can reach this supervisor at")
	} else {
		log.Info("SUPERVISOR.CALLBACKADVERTISEHOST.RESOLVED",
			"advertise_host", advertiseHost,
			"source", advertiseHostSource,
			"advertise_port", advertisePort)
	}

	// @concept: service-address-book
	staticResolver := executor.NewStaticResolver(nil)

	execServices := make([]observability.ServiceSpec, 0, len(rimskyCfg.Executors.Executors))
	for name, e := range rimskyCfg.Executors.Executors {
		// @decision: service-auth-mtls
		execServices = append(execServices, observability.ServiceSpec{
			Name:                  name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
			TLS:                   e.TLS,
		})
	}
	disc := observability.RunHandshake(ctx, observability.NewGRPCProber(), execServices, nil, logger)

	if bundledRegs != nil {
		mergeBundledExecutorAliases(staticResolver, configuredExecutors, bundledRegs.ExecutorAliases, func(name string) {
			log.Info("BUNDLED.EXECUTOR.OVERRIDDEN", "executor", name)
		})
		bundledRegs.AdvertiseInto(disc, rimskyCfg.Executors.Executors, storesCfg.ClaimProducers)
	}

	// @concept: service-address-book
	addressBookResolver := executor.NewAddressBookResolver(
		staticResolver, config.AddressBookExecutorLookup(driver.Tables()), 0, nil)
	resolver := buildSupervisorResolver(addressBookResolver, rimskyCfg.LateBindServiceProxies, driver.Tables())

	mreg := observability.NewMetricsRegistry()

	lateBindProxies := rimskyCfg.LateBindServiceProxies

	h, err := config.StartSupervisor(config.SupervisorConfig{
		SupervisorID:                supID,
		Driver:                      driver,
		Clock:                       shared.SystemClock{},
		Logger:                      log,
		Concurrency:                 concurrency,
		LivenessInterval:            resolved.LivenessInterval,
		ClaimPollInterval:           resolved.ClaimPollInterval,
		SyncRPCDeadlineDefault:      rimskyCfg.Dispatch.SyncRPCDeadlineDefault,
		MaxQuietPeriodDefault:       rimskyCfg.Dispatch.MaxQuietPeriodDefault,
		MaxRuntimeDefault:           rimskyCfg.Dispatch.MaxRuntimeDefault,
		Resolver:                    resolver,
		ClaimProducers:              storesCfg,
		Publishers:                  rimskyCfg.Publishers,
		NamedLocks:                  namedLocksCfg,
		Validators:                  rimskyCfg.Validators,
		DataProcessors:              rimskyCfg.DataProcessors,
		CallbackHost:                callbackHost,
		CallbackPort:                callbackPort,
		CallbackAdvertiseHost:       advertiseHost,
		CallbackAdvertisePort:       advertisePort,
		ExpectedAttributesSchemaFor: observability.NewExpectedAttributesSchemaResolver(disc),
		Metrics:                     observability.MetricsHookOf(mreg),
		Executors:                   rimskyCfg.Executors,
		LateBindServiceProxies:      lateBindProxies,
		Bundled:                     opts.Bundled,
		// @decision: lifecycle-drain-per-role
		SharedLifecycleDrain: opts.SharedLifecycleDrain,
		ServiceAuth:          rimskyCfg.ServiceAuth,
		// @decision: lifecycle-subscriber-at-least-once-delivery
		ServiceDeliveryStallAfter: rimskyCfg.ServiceDelivery.StallAfter,
	})
	if err != nil {
		log.Error("SUPERVISOR.ROLE.STARTFAILED", "site", "StartSupervisor", "error", err.Error())
		return nil, nil, err
	}
	log.Info("SUPERVISOR.ROLE.STARTED", "id", supID, "callback_addr", h.CallbackAddr())
	reprobeServicesNowThatTheIdentityIsInstalled(ctx, disc, rimskyCfg.ServiceAuth, logger)

	gaugeCtx, cancelGauges := context.WithCancel(context.Background())
	if mhook := observability.MetricsHookOf(mreg); mhook != nil {
		mhook.StartGaugeRefresher(gaugeCtx, driver.Tables(), driver.Queue(), 0, log)
	}

	go disc.RefreshLoop(gaugeCtx, rimskyCfg.ObservabilityRefreshInterval, logger)

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
			log.Error("SUPERVISOR.ROLE.SHUTDOWNFAILED", "error", err.Error())
			firstErr = err
		}
		if metricsSrv != nil {
			if err := metricsSrv.Shutdown(stopCtx); err != nil {
				log.Warn("METRICS.SERVER.SHUTDOWNFAILED", "error", err.Error())
			}
		}
		cancelGauges()
		reporter.Close()
		return firstErr
	}
	return stop, reporter.ch, nil
}

func buildSupervisorResolver(staticResolver executor.Resolver, lateBindProxies map[string]string, persistTables persistence.Tables) executor.Resolver {
	if len(lateBindProxies) == 0 {
		return staticResolver
	}
	lookupBindings := func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error) {
		return config.LookupInstanceBindings(ctx, persistTables, instanceID, nil)
	}
	return executor.NewLateBindResolver(staticResolver, lookupBindings, lateBindProxies)
}

func mergeBundledExecutorAliases(resolver *executor.StaticResolver, configured map[string]executor.Endpoint, bundled map[string]executor.Endpoint, onOverride func(name string)) {
	for name, ep := range bundled {
		if _, exists := configured[name]; exists {
			if onOverride != nil {
				onOverride(name)
			}
			continue
		}
		resolver.Register(name, ep)
	}
}

// @concept: service-auth
// @decision: service-auth-mtls
func reprobeServicesNowThatTheIdentityIsInstalled(ctx context.Context, disc *observability.Discovery, serviceAuth string, log *slog.Logger) {
	if serviceAuth != service.ServiceAuthMTLS {
		return
	}
	disc.Refresh(ctx, log)
}
