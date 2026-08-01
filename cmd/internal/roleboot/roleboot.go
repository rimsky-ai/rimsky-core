// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package roleboot

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const shutdownDeadline = 30 * time.Second

type RoleRunner func(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig) (launch.StopFunc, <-chan error, error)

func Main(run RoleRunner) {
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

	stop, failCh, err := run(ctx, logger, driver, cfg)
	if err != nil {
		_ = driver.Close()
		os.Exit(1)
	}

	roleErr := shared.WaitForSignalOrFailure(log, sigCh, failCh)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownDeadline)
	defer cancel()
	_ = stop(shutdownCtx)
	if roleErr != nil {
		_ = driver.Close()
		os.Exit(1)
	}
}
