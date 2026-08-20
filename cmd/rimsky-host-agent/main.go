// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

func main() {
	cfg, err := hostagent.LoadConfigFromEnv()
	if err != nil {
		slog.Error("hostagent.LoadConfigFromEnv", "error", err)
		os.Exit(1)
	}

	logger := serverkit.NewJSONLoggerForLevel(cfg.LogLevel)
	slog.SetDefault(logger)

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), logger)
	defer stopSignals()

	if err := hostagent.Run(ctx, cfg); err != nil {
		slog.Error("hostagent.Run", "error", err)
		os.Exit(1)
	}
}
