// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

func main() {
	slogLogger := serverkit.NewJSONLogger()
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		slogLogger = slogLogger.With("binary", name)
	}
	slog.SetDefault(slogLogger)

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), slogLogger)
	defer stopSignals()

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
