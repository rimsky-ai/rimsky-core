// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// rimsky-openlineage — bundled OpenLineage subscriber reference impl.
// Polls `table:rimsky_lineage` for new records since a stored cursor
// and emits OpenLineage 1.x JSON events to a configured backend
// (Marquez, DataHub, …).
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §OpenLineage emitter.
//
//	@concept: lineage
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	log := slog.Default()

	cfg, err := LoadConfig()
	if err != nil {
		log.Error("openlineage.config_invalid", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := New(ctx, cfg, log)
	if err != nil {
		log.Error("openlineage.startup_failed", "error", err.Error())
		os.Exit(1)
	}
	defer sub.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("openlineage.stopping")
		cancel()
	}()

	sub.Run(ctx)
}
