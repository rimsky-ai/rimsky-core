// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package launch

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"

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

func RunScheduler(ctx context.Context, logger *slog.Logger, driver persistence.Database, rimskyCfg *config.RimskyConfig, preOpenedBlob persistence.BlobBackend) (StopFunc, <-chan error, error) {
	log := shared.NewSlogLogger(logger)

	tickMs, err := positiveIntEnv("RIMSKY_SCHEDULER_TICK_MS", 250)
	if err != nil {
		log.Error("scheduler tick_ms resolution", "error", err.Error())
		return nil, nil, err
	}

	metricsPort, err := metricsPortFor("scheduler", rimskyCfg.Topology)
	if err != nil {
		log.Error("metrics port resolution", "error", err.Error())
		return nil, nil, err
	}

	blobBackend := preOpenedBlob
	if blobBackend == nil {
		blobBackend, err = config.OpenBlobBackend(rimskyCfg.Blob, driver, rimskyCfg.Topology)
		if err != nil {
			log.Error("config.OpenBlobBackend", "error", err.Error())
			return nil, nil, err
		}
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

	lateBindProxies := rimskyCfg.LateBindServiceProxies
	peersForSpec := func(tplSpec node.TemplateSpec) []string {
		return controlapi.LifecyclePeersForSpec(
			controlapi.AppDeps{LateBindServiceProxies: lateBindProxies},
			tplSpec,
		)
	}

	h, err := config.StartScheduler(config.SchedulerConfig{
		Driver:                  driver,
		Clock:                   shared.SystemClock{},
		Logger:                  log,
		TickInterval:            time.Duration(tickMs) * time.Millisecond,
		ClaimProducers:          rimskyCfg.ClaimProducers,
		Executors:               rimskyCfg.Executors,
		Publishers:              rimskyCfg.Publishers,
		NamedLocks:              rimskyCfg.NamedLocks,
		SupervisorID:            schedulerID,
		Blob:                    blobBackend,
		OrphanBlobSweepInterval: rimskyCfg.Blob.Retention.OrphanSweepInterval,
		Metrics:                 observability.MetricsHookOf(mreg),
		Retention:               rimskyCfg.Retention,
		LifecyclePeersForSpec:   peersForSpec,
	})
	if err != nil {
		log.Error("StartScheduler", "error", err.Error())
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
			log.Error("scheduler shutdown", "error", err.Error())
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

func metricsPortFor(role string, topology persistence.Topology) (int, error) {
	perRoleVar := "RIMSKY_METRICS_PORT_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_"))
	if s := os.Getenv(perRoleVar); s != "" {
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
		log.Error("metrics endpoint bind", "error", err.Error(), "role", role, "addr", addr)
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
		log.Info("metrics endpoint listening", "addr", srv.Addr, "role", role)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("metrics endpoint", "error", err.Error(), "role", role)
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
