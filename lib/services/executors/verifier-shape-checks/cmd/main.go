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
	verifiershapechecks "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks"
)

func main() {
	slog.SetDefault(serverkit.NewJSONLogger())
	opts, err := verifiershapechecks.LoadOptsFromEnv()
	if err != nil {
		slog.Error("VERIFIERSHAPECHECKS.CONFIG.INVALID", "error", err.Error())
		os.Exit(1)
	}
	slog.Info("VERIFIERSHAPECHECKS.PROCESS.STARTING", "grpc_port", opts.Port, "stub_mode", opts.StubMode)

	lis, err := serverkit.Listen(opts.Host, opts.Port)
	if err != nil {
		slog.Error("VERIFIERSHAPECHECKS.GRPC.LISTENFAILED", "error", err.Error())
		os.Exit(1)
	}
	srv, identity, err := serviceauth.NewGRPCServer(context.Background(), "verifier-shape-checks")
	if err != nil {
		slog.Error("VERIFIERSHAPECHECKS.SERVICEAUTH.ENROLLFAILED", "error", err.Error())
		os.Exit(1)
	}
	genv1.RegisterExecutorServer(srv, verifiershapechecks.NewServer(opts.StubMode))
	verifiershapechecks.RegisterObservability(srv)
	genv1.RegisterValidationServer(srv, verifiershapechecks.NewValidationServer())

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), slog.Default())
	defer stopSignals()
	identity.StartMaintain(ctx, "verifier-shape-checks")
	serverkit.RunGRPC(ctx, srv, lis, "verifier-shape-checks")
}
