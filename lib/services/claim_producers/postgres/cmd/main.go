// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	opts, err := server.LoadOptsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: %v\n", err)
		os.Exit(1)
	}
	if !opts.Configured {
		fmt.Fprintf(os.Stderr, "store-postgres: missing %s (path to YAML)\n", server.ConfigEnv)
		os.Exit(1)
	}

	grpcLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", opts.Host, opts.GRPCPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: grpc listen: %v\n", err)
		os.Exit(1)
	}
	httpLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", opts.Host, opts.HTTPPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: http listen: %v\n", err)
		os.Exit(1)
	}
	var adminLis net.Listener
	if opts.AdminPort > 0 {
		adminLis, err = net.Listen("tcp", fmt.Sprintf("%s:%d", opts.Host, opts.AdminPort))
		if err != nil {
			fmt.Fprintf(os.Stderr, "store-postgres: admin listen: %v\n", err)
			os.Exit(1)
		}
	}

	slog.Info("store-postgres started",
		"grpc_addr", grpcLis.Addr().String(),
		"http_addr", httpLis.Addr().String(),
		"admin_port", opts.AdminPort,
		"pick_policies", len(opts.PickPolicies),
		"sweep_interval", opts.SweepInterval)

	ctx, cancel := signalContext()
	defer cancel()

	if err := server.Run(ctx, opts.ServerConfig(), grpcLis, httpLis, adminLis); err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: server.Run: %v\n", err)
		os.Exit(1)
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}
