// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//	@concept: lineage
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

func main() {
	ops.Setup(slog.LevelInfo)
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
