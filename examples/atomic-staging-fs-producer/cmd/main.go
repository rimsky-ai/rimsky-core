// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Reference binary for the atomic-staging pattern. Boots a gRPC
// ClaimProducer server backed by the atomic-staging filesystem Store
// plus a periodic sweep loop reaping leaked staging directories.
//
// Env vars:
//   - RIMSKY_ATOMIC_STAGING_ROOT — filesystem path (required)
//   - RIMSKY_LISTEN_ADDR        — gRPC listen address (default :8090)
//   - RIMSKY_SWEEP_INTERVAL     — sweep cadence (default 5m)
//   - RIMSKY_SWEEP_TTL          — staging TTL before sweep eligibility
//     (default 24h)
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/server"
	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store"
	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/sweep"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// emptyHandleSet is the default for `Live`. Production wiring should
// replace this with a Postgres-backed query against rimsky_claim_handles.
type emptyHandleSet struct{}

func (emptyHandleSet) Contains(string) bool { return false }

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	root := os.Getenv("RIMSKY_ATOMIC_STAGING_ROOT")
	if root == "" {
		logger.Error("RIMSKY_ATOMIC_STAGING_ROOT is required")
		os.Exit(2)
	}
	addr := os.Getenv("RIMSKY_LISTEN_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	sweepInterval, _ := time.ParseDuration(os.Getenv("RIMSKY_SWEEP_INTERVAL"))
	sweepTTL, _ := time.ParseDuration(os.Getenv("RIMSKY_SWEEP_TTL"))

	st, err := store.New(root)
	if err != nil {
		logger.Error("store init failed", "error", err.Error())
		os.Exit(1)
	}
	srv := server.New(st)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("listen failed", "addr", addr, "error", err.Error())
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(grpcSrv, srv)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	sweeper := &sweep.Sweeper{
		Store:    st,
		Live:     emptyHandleSet{},
		TTL:      sweepTTL,
		Interval: sweepInterval,
		Logger: func(format string, args ...any) {
			logger.Warn("sweep", "msg", format, "args", args)
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		logger.Info("atomic-staging serving", "addr", addr, "root", root)
		if err := grpcSrv.Serve(lis); err != nil {
			logger.Error("grpc serve", "error", err.Error())
		}
	}()
	go func() {
		defer wg.Done()
		_ = sweeper.Run(ctx)
	}()

	<-ctx.Done()
	grpcSrv.GracefulStop()
	wg.Wait()
}
