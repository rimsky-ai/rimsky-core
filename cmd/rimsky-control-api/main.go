// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-control-api is the env-var-driven entry point for the control
// API HTTP server. Reads RIMSKY_CONFIG (persistence + stores +
// named_locks + executors per docs/specs/2026-05-01-control-plane-and-
// store-lifecycle-design.md §3.1 and 2026-05-02-persistence-pluggable-
// and-unified-image-design.md §8) and calls config.StartControlAPI
// which dials each remote store-service.
//
// Environment variables:
//
//	RIMSKY_CONFIG            optional; path to rimsky.yml.
//	                         default /etc/rimsky/rimsky.yml.
//	RIMSKY_CONTROL_API_HOST  optional; default 127.0.0.1.
//	RIMSKY_CONTROL_API_PORT  optional; default 8080 (0 = OS-assigned).
//	RIMSKY_METRICS_PORT      optional; default 0 = disabled. When >0
//	                         exposes /metrics on this port (Prometheus
//	                         text format) on the same host as the
//	                         control API.
//	RIMSKY_LOG_LEVEL         optional; debug|info|warn|error (default info).
//	RIMSKY_LOG_BINARY        optional; structured slog field for unified-image.
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

	"github.com/fallguyconsulting/rimsky/control/config"
	"github.com/fallguyconsulting/rimsky/control/observability"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	_ "github.com/fallguyconsulting/rimsky/foundation/persistence/postgres" // register driver
	_ "github.com/fallguyconsulting/rimsky/foundation/persistence/sqlite"   // register driver
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// defaultRimskyConfigPath is the path used when RIMSKY_CONFIG is unset.
const defaultRimskyConfigPath = "/etc/rimsky/rimsky.yml"

func main() {
	host := os.Getenv("RIMSKY_CONTROL_API_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port, _ := strconv.Atoi(os.Getenv("RIMSKY_CONTROL_API_PORT"))
	if port == 0 {
		port = 8080
	}
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

	// Install BlobBackend on the driver so attribute writes from
	// control-api (e.g. instance-create-time fixture seeding via raw
	// store calls) honor the spill threshold. Validation is identical
	// across the three processes via ValidateBlobConfig.
	if _, err := config.OpenBlobBackend(rimskyCfg.Blob, driver); err != nil {
		log.Error("config.OpenBlobBackend", "error", err.Error())
		_ = driver.Close()
		os.Exit(1)
	}

	// Plan I1/I2: per-process Prometheus registry. Constructed up-front
	// so the control-api's admin-invalidate path can be instrumented
	// via the MetricsHook adapter. The /metrics HTTP listener is opened
	// below only when RIMSKY_METRICS_PORT > 0; the registry itself is
	// built unconditionally so the hook stays wired even when the HTTP
	// surface is disabled.
	mreg := observability.NewMetricsRegistry()

	h, err := config.StartControlAPI(config.ControlAPIConfig{
		Driver:     driver,
		Clock:      shared.SystemClock{},
		Logger:     log,
		Host:       host,
		Port:       port,
		Stores:     rimskyCfg.Stores,
		NamedLocks: rimskyCfg.NamedLocks,
		Executors:  rimskyCfg.Executors,
		Metrics:    observability.MetricsHookOf(mreg),

		LateBindServiceProxies: rimskyCfg.LateBindServiceProxies,
	})
	if err != nil {
		log.Error("StartControlAPI", "error", err.Error())
		_ = driver.Close()
		os.Exit(1)
	}
	log.Info("control api listening", "addr", h.Addr())

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
	metricsPort, _ := strconv.Atoi(os.Getenv("RIMSKY_METRICS_PORT"))
	var metricsSrv *http.Server
	if metricsPort > 0 {
		metricsRouter := chi.NewRouter()
		observability.MountMetrics(metricsRouter, mreg)
		metricsSrv = &http.Server{
			Addr:              fmt.Sprintf("%s:%d", host, metricsPort),
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
		log.Error("control api shutdown", "error", err.Error())
	}
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
	_ = driver.Close()
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
