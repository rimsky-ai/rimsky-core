// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
)

func main() {
	cfg, err := hostdaemon.LoadConfigFromEnv()
	if err != nil {
		slog.Error("HOSTDAEMON.CONFIG.LOADFAILED", "site", "hostdaemon.LoadConfigFromEnv", "error", err)
		os.Exit(1)
	}

	logger := serverkit.NewJSONLoggerForLevel(cfg.LogLevel)
	slog.SetDefault(logger)

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), logger)
	defer stopSignals()

	if err := hostdaemon.Run(ctx, cfg); err != nil {
		slog.Error("HOSTDAEMON.PROCESS.RUNFAILED", "site", "hostdaemon.Run", "error", err)
		os.Exit(1)
	}
}
