// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	verifierhttp "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
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
	srv, identity, err := peerauth.NewGRPCServer(context.Background(), "verifier-http")
	if err != nil {
		slog.Error("verifier-http peer-auth", "error", err.Error())
		os.Exit(1)
	}
	genv1.RegisterExecutorServer(srv, verifierhttp.NewServer(opts.StubMode))
	verifierhttp.RegisterObservability(srv)

	ctx, cancel := context.WithCancel(context.Background())
	identity.StartMaintain(ctx, "verifier-http")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("verifier-http stopping")
		cancel()
	}()
	serverkit.RunGRPC(ctx, srv, lis, "verifier-http")
}
