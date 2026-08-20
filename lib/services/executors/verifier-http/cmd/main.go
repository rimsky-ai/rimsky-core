// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	verifierhttp "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http"
)

func main() {
	slog.SetDefault(serverkit.NewJSONLogger())
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
	genv1.RegisterExecutorServer(srv, verifierhttp.NewServer(opts))
	verifierhttp.RegisterObservability(srv)

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), slog.Default())
	defer stopSignals()
	identity.StartMaintain(ctx, "verifier-http")
	serverkit.RunGRPC(ctx, srv, lis, "verifier-http")
}
