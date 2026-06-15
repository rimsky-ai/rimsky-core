// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

// main verifier executor — protocol-shape reference impl. Env vars
// follow the bundled-executor pattern (`RIMSKY_EXECUTOR_<NAME>_*`).
//
// @deliberate: implements the verifier-executor pattern
// (documentation-only, no successor concept).
func main() {
	host := envOr("RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_HOST", "0.0.0.0")
	port := atoiOr("RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_PORT", 9095)
	stubMode := os.Getenv("RIMSKY_EXECUTOR_STUB_MODE") == "1"

	ops.Setup(slog.LevelInfo)
	slog.Info("verifier-shape-checks starting", "grpc_port", port, "stub_mode", stubMode)

	lis, err := serverkit.Listen(host, port)
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, NewServer(stubMode))
	RegisterObservability(srv)
	// @constraint: verifier-shape-checks advertises role="executor" validation
	// alongside its executor role so rimsky's control-api can cross-check the
	// resolved attribute schema at template registration.
	genv1.RegisterValidationServer(srv, NewValidationServer())

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("verifier-shape-checks stopping")
		cancel()
	}()
	serverkit.RunGRPC(ctx, srv, lis, "verifier-shape-checks")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoiOr(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
