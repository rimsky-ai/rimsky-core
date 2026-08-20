// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package roleboot

import (
	"context"
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

type RoleRunner func(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig) (launch.StopFunc, <-chan error, error)

func Main(run RoleRunner) {
	logger := serverkit.NewJSONLogger()
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		logger = logger.With("binary", name)
	}
	slog.SetDefault(logger)
	log := shared.NewSlogLogger(logger)

	sigCh, stopNotify := serverkit.NotifyShutdownSignals()
	defer stopNotify()

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

	// @decision: graceful-shutdown
	drained := make(chan struct{})
	defer close(drained)
	serverkit.InstallSecondSignalHardExit(sigCh, drained, logger, func() { os.Exit(serverkit.HardExitCode) })

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serverkit.DeployedCoreGrace)
	defer cancel()
	_ = stop(shutdownCtx)
	if roleErr != nil {
		_ = driver.Close()
		os.Exit(1)
	}
}
