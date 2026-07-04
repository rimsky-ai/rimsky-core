// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	verifierhttp "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

func main() {
	ops.Setup(slog.LevelInfo)
	opts, err := verifierhttp.LoadOptsFromEnv()
	if err != nil {
		slog.Error("verifier-http config", "error", err.Error())
		os.Exit(1)
	}
	slog.Info("verifier-http starting", "grpc_port", opts.Port, "stub_mode", opts.StubMode)

	lis, err := serverkit.Listen(opts.Host, opts.Port)
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, verifierhttp.NewServer(opts.StubMode))
	verifierhttp.RegisterObservability(srv)

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("verifier-http stopping")
		cancel()
	}()
	serverkit.RunGRPC(ctx, srv, lis, "verifier-http")
}
