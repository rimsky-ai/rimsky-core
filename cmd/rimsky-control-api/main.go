// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: shared.ParseLogLevel(os.Getenv("RIMSKY_LOG_LEVEL"))})
	logger := slog.New(handler)
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		logger = logger.With("binary", name)
	}
	slog.SetDefault(logger)
	log := shared.NewSlogLogger(logger)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ctx := context.Background()
	driver, cfg, err := launch.OpenDriverFromEnv(ctx, logger)
	if err != nil {
		os.Exit(1)
	}
	defer func() { _ = driver.Close() }()

	stop, failCh, err := launch.RunControlAPI(ctx, logger, driver, cfg, nil, nil)
	if err != nil {
		_ = driver.Close()
		os.Exit(1)
	}

	roleErr := shared.WaitForSignalOrFailure(log, sigCh, failCh)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = stop(shutdownCtx)
	if roleErr != nil {
		_ = driver.Close()
		os.Exit(1)
	}
}
