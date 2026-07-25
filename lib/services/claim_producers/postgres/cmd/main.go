// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/server"
	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/shared/runner"
)

const serviceName = "claim-producer-postgres"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	opts, err := server.LoadOptsFromEnv()
	if err != nil {
		runner.Fatal(serviceName, err)
	}
	if !opts.Configured {
		runner.Fatal(serviceName, fmt.Errorf("missing %s (path to YAML)", server.ConfigEnv))
	}

	grpcLis, httpLis, adminLis, err := runner.Listen(opts.Host, opts.GRPCPort, opts.HTTPPort, opts.AdminPort)
	if err != nil {
		runner.Fatal(serviceName, err)
	}

	slog.Info(serviceName+" started",
		"grpc_addr", grpcLis.Addr().String(),
		"http_addr", httpLis.Addr().String(),
		"admin_port", opts.AdminPort,
		"pick_policies", len(opts.PickPolicies),
		"sweep_interval", opts.SweepInterval)

	ctx, cancel := runner.SignalContext()
	defer cancel()

	if err := server.Run(ctx, opts.ServerConfig(), grpcLis, httpLis, adminLis); err != nil {
		runner.Fatal(serviceName, fmt.Errorf("server.Run: %w", err))
	}
}
