// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package launch

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"

	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type StopFunc func(ctx context.Context) error

type failureReporter struct {
	mu     sync.Mutex
	closed bool
	ch     chan error
}

func newFailureReporter(capacity int) *failureReporter {
	return &failureReporter{ch: make(chan error, capacity)}
}

func (f *failureReporter) Report(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	select {
	case f.ch <- err:
	default:
	}
}

func (f *failureReporter) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	close(f.ch)
}

func RunScheduler(ctx context.Context, logger *slog.Logger, driver persistence.Database, rimskyCfg *config.RimskyConfig, opts RoleOptions) (StopFunc, <-chan error, error) {
	log := shared.NewSlogLogger(logger)

	tickMs, err := positiveIntEnv("RIMSKY_SCHEDULER_TICK_MS", 250)
	if err != nil {
		log.Error("SCHEDULER.TICKINTERVAL.RESOLVEFAILED", "error", err.Error())
		return nil, nil, err
	}

	metricsPort, err := metricsPortFor("scheduler", rimskyCfg.Topology)
	if err != nil {
		log.Error("METRICS.PORT.RESOLVEFAILED", "error", err.Error())
		return nil, nil, err
	}

	schedulerID := os.Getenv("RIMSKY_SCHEDULER_ID")
	if schedulerID == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			schedulerID = "scheduler-" + hostname
		} else {
			schedulerID = "scheduler-default"
		}
	}

	mreg := observability.NewMetricsRegistry()

	h, err := config.StartScheduler(config.SchedulerConfig{
		Driver:                 driver,
		Clock:                  shared.SystemClock{},
		Logger:                 log,
		TickInterval:           time.Duration(tickMs) * time.Millisecond,
		MaxQuietPeriodDefault:  rimskyCfg.Dispatch.MaxQuietPeriodDefault,
		MaxRuntimeDefault:      rimskyCfg.Dispatch.MaxRuntimeDefault,
		ClaimProducers:         rimskyCfg.ClaimProducers,
		Executors:              rimskyCfg.Executors,
		Publishers:             rimskyCfg.Publishers,
		NamedLocks:             rimskyCfg.NamedLocks,
		SupervisorID:           schedulerID,
		Metrics:                observability.MetricsHookOf(mreg),
		Retention:              rimskyCfg.Retention,
		LateBindServiceProxies: rimskyCfg.LateBindServiceProxies,
		ServiceAuth:            rimskyCfg.ServiceAuth,
		// @decision: lifecycle-drain-per-role
		SharedLifecycleDrain: opts.SharedLifecycleDrain,
		// @decision: lifecycle-subscriber-at-least-once-delivery
		ServiceDeliveryStallAfter: rimskyCfg.ServiceDelivery.StallAfter,
	})
	if err != nil {
		log.Error("SCHEDULER.ROLE.STARTFAILED", "site", "StartScheduler", "error", err.Error())
		return nil, nil, err
	}

	gaugeCtx, cancelGauges := context.WithCancel(context.Background())
	if mhook := observability.MetricsHookOf(mreg); mhook != nil {
		mhook.StartGaugeRefresher(gaugeCtx, driver.Tables(), driver.Queue(), 0, log)
	}

	reporter := newFailureReporter(1)
	metricsSrv := startMetricsServer(metricsHostFromEnv(), "scheduler", metricsPort, mreg, log, reporter)

	stop := func(stopCtx context.Context) error {
		var firstErr error
		if err := h.Shutdown(stopCtx); err != nil {
			log.Error("SCHEDULER.ROLE.SHUTDOWNFAILED", "error", err.Error())
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

func metricsHostFromEnv() string {
	host := os.Getenv("RIMSKY_METRICS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	return host
}

var metricsRoleOffsets = map[string]int{
	"scheduler":   0,
	"supervisor":  1,
	"control-api": 2,
}

var metricsPortEnvVars = map[string]string{
	"scheduler":   "RIMSKY_METRICS_PORT_SCHEDULER",
	"supervisor":  "RIMSKY_METRICS_PORT_SUPERVISOR",
	"control-api": "RIMSKY_METRICS_PORT_CONTROL_API",
}

func metricsPortFor(role string, topology persistence.Topology) (int, error) {
	perRoleVar := metricsPortEnvVars[role]
	if s := os.Getenv(perRoleVar); perRoleVar != "" && s != "" {
		port, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("invalid %s=%q: not a number", perRoleVar, s)
		}
		return port, nil
	}
	baseStr := os.Getenv("RIMSKY_METRICS_PORT")
	if baseStr == "" {
		return 0, nil
	}
	base, err := strconv.Atoi(baseStr)
	if err != nil {
		return 0, fmt.Errorf("invalid RIMSKY_METRICS_PORT=%q: not a number", baseStr)
	}
	if base <= 0 {
		return 0, nil
	}
	if topology.Unified() {
		return base + metricsRoleOffsets[role], nil
	}
	return base, nil
}

func startMetricsServer(host, role string, metricsPort int, mreg *observability.MetricsRegistry, log shared.Logger, report *failureReporter) *http.Server {
	if metricsPort <= 0 {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", host, metricsPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("METRICS.ENDPOINT.BINDFAILED", "error", err.Error(), "role", role, "addr", addr)
		if report != nil {
			report.Report(fmt.Errorf("%s metrics endpoint bind %s: %w", role, addr, err))
		}
		return nil
	}
	return serveMetrics(ln, role, mreg, log, report)
}

func serveMetrics(ln net.Listener, role string, mreg *observability.MetricsRegistry, log shared.Logger, report *failureReporter) *http.Server {
	metricsRouter := chi.NewRouter()
	observability.MountMetrics(metricsRouter, mreg)
	srv := &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           metricsRouter,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("METRICS.ENDPOINT.LISTENING", "addr", srv.Addr, "role", role)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("METRICS.ENDPOINT.SERVEFAILED", "error", err.Error(), "role", role)
			if report != nil {
				report.Report(fmt.Errorf("%s metrics endpoint: %w", role, err))
			}
		}
	}()
	return srv
}

func positiveIntEnv(name string, dflt int) (int, error) {
	s := os.Getenv(name)
	if s == "" {
		return dflt, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: not a number", name, s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid %s=%q: must be a positive integer", name, s)
	}
	return n, nil
}
