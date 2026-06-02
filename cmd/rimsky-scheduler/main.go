// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-scheduler is the env-var-driven entry point for the
// scheduler process. Builds a typed config.SchedulerConfig from
// environment variables, loads the unified deployment-shape config
// from RIMSKY_CONFIG (persistence + stores + named_locks + executors per
// docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md
// §3.1 and 2026-05-02-persistence-pluggable-and-unified-image-design.md
// §8), and calls config.StartScheduler.
//
// Environment variables:
//
//	RIMSKY_CONFIG               optional; default /etc/rimsky/rimsky.yml.
//	RIMSKY_SCHEDULER_TICK_MS    optional; default 1500.
//	RIMSKY_HEARTBEAT_TIMEOUT_MS optional; default 15000.
//	RIMSKY_METRICS_PORT         optional; default 0 = disabled. When >0
//	                            exposes /metrics on this port (Prometheus
//	                            text format) bound to RIMSKY_METRICS_HOST
//	                            (default 127.0.0.1).
//	RIMSKY_METRICS_HOST         optional; default 127.0.0.1.
//	RIMSKY_LOG_LEVEL            optional; debug|info|warn|error (default info).
//	RIMSKY_LOG_BINARY           optional; structured slog field for unified-image.
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

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres" // register driver
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"   // register driver
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// defaultRimskyConfigPath is the path used when RIMSKY_CONFIG is unset.
const defaultRimskyConfigPath = "/etc/rimsky/rimsky.yml"

func main() {
	tickMs := atoiDefault(os.Getenv("RIMSKY_SCHEDULER_TICK_MS"), 1500)
	heartbeatMs := atoiDefault(os.Getenv("RIMSKY_HEARTBEAT_TIMEOUT_MS"), 15000)
	logLevel := os.Getenv("RIMSKY_LOG_LEVEL")

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(logLevel)})
	logger := slog.New(handler)
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		logger = logger.With("binary", name)
	}
	slog.SetDefault(logger)
	log := shared.NewSlogLogger(logger)

	configPath := os.Getenv("RIMSKY_CONFIG")
	if configPath == "" {
		configPath = defaultRimskyConfigPath
	}
	rimskyCfg, err := config.LoadRimskyConfigYAML(configPath)
	if err != nil {
		log.Error("load rimsky config", "error", err.Error(), "path", configPath)
		os.Exit(1)
	}

	ctx := context.Background()
	driver, err := persistence.Open(ctx, rimskyCfg.Persistence)
	if err != nil {
		log.Error("persistence.Open", "error", err.Error())
		os.Exit(1)
	}

	// Install BlobBackend on the driver. The scheduler does not itself
	// spill writes (it reads via SweepParkedNodes which hits parked
	// payload columns, but those go through queue.LoadResumeMetadataInTx
	// at the supervisor side). Installing here keeps ValidateBlobConfig
	// gating consistent across processes (memory backend rejection,
	// filesystem.root presence) and exposes the backend on the driver
	// in case future scheduler-side sweeps need it.
	blobBackend, err := config.OpenBlobBackend(rimskyCfg.Blob, driver)
	if err != nil {
		log.Error("config.OpenBlobBackend", "error", err.Error())
		_ = driver.Close()
		os.Exit(1)
	}

	supervisorID := os.Getenv("RIMSKY_SCHEDULER_ID")
	if supervisorID == "" {
		// Hostname-derived default so multi-replica deployments don't
		// collide on a single shared id. The scheduler-tick advisory
		// lock still single-writes against the scheduler tick, but a
		// per-replica id keeps audit-log rows and orphan-claim
		// attribution honest.
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			supervisorID = "scheduler-" + hostname
		} else {
			supervisorID = "scheduler-default"
		}
	}

	// Plan I1/I2: per-process Prometheus registry. Constructed up-front
	// so the scheduler's per-tick invalidate emits and frame.RunTick
	// observations land on the shared registry via the MetricsHook
	// adapter. The /metrics HTTP listener is opened below only when
	// RIMSKY_METRICS_PORT > 0; the registry itself is built
	// unconditionally so the hook stays wired even when the HTTP
	// surface is disabled (e.g. unified-image deployments that scrape
	// a sibling process's port).
	mreg := observability.NewMetricsRegistry()

	h, err := config.StartScheduler(config.SchedulerConfig{
		Driver:                  driver,
		Clock:                   shared.SystemClock{},
		Logger:                  log,
		TickInterval:            time.Duration(tickMs) * time.Millisecond,
		HeartbeatTimeout:        time.Duration(heartbeatMs) * time.Millisecond,
		Stores:                  rimskyCfg.Stores,
		NamedLocks:              rimskyCfg.NamedLocks,
		SupervisorID:            supervisorID,
		Blob:                    blobBackend,
		OrphanBlobSweepInterval: rimskyCfg.Blob.Retention.OrphanSweepInterval,
		Metrics:                 observability.MetricsHookOf(mreg),
		Retention:               rimskyCfg.Retention,
	})
	if err != nil {
		log.Error("StartScheduler", "error", err.Error())
		_ = driver.Close()
		os.Exit(1)
	}

	// Plan I2: launch the gauge refresher so node-state, parked-by-reason,
	// held-frames, and dispatch-queue-depth gauges reflect live persistence
	// state. The refresher polls every 5s by default; cancel on shutdown.
	gaugeCtx, cancelGauges := context.WithCancel(context.Background())
	defer cancelGauges()
	if mhook := observability.MetricsHookOf(mreg); mhook != nil {
		mhook.StartGaugeRefresher(gaugeCtx, driver.Tables(), driver.Queue(), 0, log)
	}

	// Optional Prometheus /metrics endpoint on a separate port.
	// Plan I1: gated by RIMSKY_METRICS_PORT (0 = disabled).
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
		log.Error("scheduler shutdown", "error", err.Error())
	}
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
	_ = driver.Close()
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return d
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
