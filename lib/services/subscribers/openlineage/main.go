// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: lineage
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

func main() {
	slog.SetDefault(serverkit.NewJSONLogger())
	log := slog.Default()

	cfg, err := LoadConfig()
	if err != nil {
		log.Error("OPENLINEAGE.CONFIG.INVALID", "error", err.Error())
		os.Exit(1)
	}

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), log)
	defer stopSignals()

	sub, err := New(ctx, cfg, log)
	if err != nil {
		log.Error("OPENLINEAGE.PROCESS.STARTFAILED", "error", err.Error())
		os.Exit(1)
	}
	defer sub.Close()

	sub.Run(ctx)
}
