// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	verifiershapechecks "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	opts, err := verifiershapechecks.LoadOptsFromEnv()
	if err != nil {
		slog.Error("verifier-shape-checks config", "error", err.Error())
		os.Exit(1)
	}
	slog.Info("verifier-shape-checks starting", "grpc_port", opts.Port, "stub_mode", opts.StubMode)

	lis, err := serverkit.Listen(opts.Host, opts.Port)
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	srv, identity, err := peerauth.NewGRPCServer(context.Background(), "verifier-shape-checks")
	if err != nil {
		slog.Error("verifier-shape-checks peer-auth", "error", err.Error())
		os.Exit(1)
	}
	genv1.RegisterExecutorServer(srv, verifiershapechecks.NewServer(opts.StubMode))
	verifiershapechecks.RegisterObservability(srv)
	genv1.RegisterValidationServer(srv, verifiershapechecks.NewValidationServer())

	ctx, cancel := context.WithCancel(context.Background())
	identity.StartMaintain(ctx, "verifier-shape-checks")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("verifier-shape-checks stopping")
		cancel()
	}()
	serverkit.RunGRPC(ctx, srv, lis, "verifier-shape-checks")
}
