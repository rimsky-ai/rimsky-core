// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
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
	logger := shared.NewSlogLogger(slogLogger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, logger); err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger shared.Logger) error {
	cfgPath := os.Getenv("RIMSKY_CONFIG")
	if cfgPath == "" {
		cfgPath = "/etc/rimsky/rimsky.yml"
	}
	cfg, err := config.LoadRimskyConfigYAML(cfgPath)
	if err != nil {
		return fmt.Errorf("load rimsky config: %w", err)
	}

	driver, err := persistence.Open(ctx, cfg.Persistence)
	if err != nil {
		return fmt.Errorf("persistence.Open: %w", err)
	}
	defer func() { _ = driver.Close() }()

	if err := driver.Migrate(ctx, logger); err != nil {
		return fmt.Errorf("driver.Migrate: %w", err)
	}
	logger.Info("migrations complete")
	return nil
}
