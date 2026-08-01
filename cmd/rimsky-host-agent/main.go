// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

func main() {
	cfg, err := hostagent.LoadConfigFromEnv()
	if err != nil {
		slog.Error("hostagent.LoadConfigFromEnv", "error", err)
		os.Exit(1)
	}

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: shared.ParseLogLevel(cfg.LogLevel)})
	slog.SetDefault(slog.New(handler))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := hostagent.Run(ctx, cfg); err != nil {
		slog.Error("hostagent.Run", "error", err)
		os.Exit(1)
	}
}
