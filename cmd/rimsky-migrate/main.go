// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func main() {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: shared.ParseLogLevel(os.Getenv("RIMSKY_LOG_LEVEL"))})
	slogLogger := slog.New(handler)
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		slogLogger = slogLogger.With("binary", name)
	}
	slog.SetDefault(slogLogger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, slogLogger); err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, slogLogger *slog.Logger) error {
	driver, _, err := launch.OpenDriverFromEnv(ctx, slogLogger)
	if err != nil {
		return err
	}
	defer func() { _ = driver.Close() }()

	logger := shared.NewSlogLogger(slogLogger)
	if err := driver.Migrate(ctx, logger); err != nil {
		return fmt.Errorf("driver.Migrate: %w", err)
	}
	logger.Info("migrations complete")
	return nil
}
