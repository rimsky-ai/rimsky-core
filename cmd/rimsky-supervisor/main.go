// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-supervisor is the YAML-configured entry point for the
// supervisor process. Builds the role's logger from env, then delegates
// the full wiring (supervisor-tuning YAML from RIMSKY_SUPERVISOR_CONFIG,
// rimsky.yml from RIMSKY_CONFIG, persistence open,
// config.StartSupervisor, background loops) to launch.RunSupervisor and
// waits for a termination signal.
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
//	RIMSKY_LOG_BINARY         optional; structured slog field for unified-image.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func main() {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("RIMSKY_LOG_LEVEL"))})
	logger := slog.New(handler)
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		logger = logger.With("binary", name)
	}
	slog.SetDefault(logger)
	log := shared.NewSlogLogger(logger)

	// Register the signal handler BEFORE the role starts: startup can be
	// slow (DB dials, handshakes), and as a container PID-1 an
	// unregistered SIGTERM during that window would be silently dropped
	// (default disposition is ignored for PID-1), hanging the container
	// until SIGKILL. The buffered channel queues a signal received
	// mid-start.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	stop, failCh, err := launch.RunSupervisor(context.Background(), logger)
	if err != nil {
		// RunSupervisor already logged / printed the failure with full
		// context (config errors go to stderr with the rimsky-supervisor
		// prefix; wiring errors go to the structured logger).
		os.Exit(1)
	}

	roleErr := waitForSignalOrFailure(log, sigCh, failCh)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = stop(shutdownCtx)
	if roleErr != nil {
		// A dead role must restart the container, not linger degraded.
		os.Exit(1)
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

// waitForSignalOrFailure blocks until a termination signal arrives
// (returns nil) or the role reports a fatal post-start failure on
// failCh (returns the error). sigCh must already be registered with
// signal.Notify — main registers it before launch.RunSupervisor so a
// SIGTERM during slow startup is queued instead of dropped.
func waitForSignalOrFailure(log shared.Logger, sigCh <-chan os.Signal, failCh <-chan error) error {
	select {
	case s := <-sigCh:
		log.Info("signal received", "signal", s.String())
		return nil
	case err := <-failCh:
		log.Error("role failed", "error", err.Error())
		return err
	}
}
