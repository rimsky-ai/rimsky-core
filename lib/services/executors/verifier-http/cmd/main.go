// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"log/slog"
	"os"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
	verifierhttp "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http"
)

func main() {
	slog.SetDefault(serverkit.NewJSONLogger())
	opts, err := verifierhttp.LoadOptsFromEnv()
	if err != nil {
		slog.Error("VERIFIERHTTP.CONFIG.INVALID", "error", err.Error())
		os.Exit(1)
	}
	slog.Info("VERIFIERHTTP.PROCESS.STARTING", "grpc_port", opts.Port, "stub_mode", opts.StubMode)

	lis, err := serverkit.Listen(opts.Host, opts.Port)
	if err != nil {
		slog.Error("VERIFIERHTTP.GRPC.LISTENFAILED", "error", err.Error())
		os.Exit(1)
	}
	srv, identity, err := serviceauth.NewGRPCServer(context.Background(), "verifier-http")
	if err != nil {
		slog.Error("VERIFIERHTTP.SERVICEAUTH.ENROLLFAILED", "error", err.Error())
		os.Exit(1)
	}
	genv1.RegisterExecutorServer(srv, verifierhttp.NewServer(opts))
	verifierhttp.RegisterObservability(srv)

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), slog.Default())
	defer stopSignals()
	identity.StartMaintain(ctx, "verifier-http")
	serverkit.RunGRPC(ctx, srv, lis, "verifier-http")
}
