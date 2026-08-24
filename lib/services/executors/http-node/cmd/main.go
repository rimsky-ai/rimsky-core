// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
	httpnode "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node"
)

// @decision: graceful-shutdown
const grpcShutdownGracePeriod = serverkit.BundledServiceGrace

func main() {
	slog.SetDefault(serverkit.NewJSONLogger())
	opts, err := httpnode.LoadOptsFromEnv()
	if err != nil {
		slog.Error("HTTPNODE.CONFIG.INVALID", "error", err.Error())
		os.Exit(1)
	}

	identity, err := serviceauth.LoadFromEnv(context.Background(), "http-node")
	if err != nil {
		slog.Error("HTTPNODE.SERVICEAUTH.ENROLLFAILED", "error", err.Error())
		os.Exit(1)
	}
	slog.Info("HTTPNODE.PROCESS.STARTING",
		"grpc_port", opts.GRPCPort,
		"http_port", opts.HTTPPort,
		"stub_mode", opts.StubMode,
		"service_auth_mtls", identity.Enabled(),
	)

	s := httpnode.NewServer(opts)

	grpcLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", opts.Host, opts.GRPCPort))
	if err != nil {
		slog.Error("HTTPNODE.GRPC.LISTENFAILED", "error", err.Error())
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer(identity.GRPCServerOptions()...)
	genv1.RegisterExecutorServer(grpcSrv, s)
	obs := httpnode.RegisterObservability(grpcSrv, opts.HTTPBridgeURL)
	s.SetObservability(obs)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Error("HTTPNODE.GRPC.SERVEFAILED", "error", err.Error())
		}
	}()

	mux := http.NewServeMux()
	httpnode.MountBridge(mux, s)
	httpnode.MountObservabilityBridge(mux, obs)
	httpAddr := fmt.Sprintf("%s:%d", opts.Host, opts.HTTPPort)
	httpLis, err := net.Listen("tcp", httpAddr)
	if err != nil {
		slog.Error("HTTPNODE.HTTP.LISTENFAILED", "error", err.Error())
		os.Exit(1)
	}
	httpSrv := httpnode.NewBridgeServer(httpAddr, mux, identity)
	go func() {
		if err := httpnode.ServeBridge(httpSrv, httpLis, identity); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTPNODE.HTTP.SERVEFAILED", "error", err.Error())
		}
	}()

	maintainCtx, cancelMaintain := context.WithCancel(context.Background())
	defer cancelMaintain()
	go identity.Maintain(maintainCtx, serviceauth.DefaultRenewCheckInterval, func(err error) {
		slog.Error("HTTPNODE.SERVICEAUTH.RENEWFAILED", "error", err.Error())
	})

	sweepCtx, cancelSweep := context.WithCancel(context.Background())
	defer cancelSweep()
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case now := <-t.C:
				obs.SweepEvicted(now)
			}
		}
	}()

	// @decision: graceful-shutdown
	shutdownCtx, stopSignals := serverkit.ShutdownContext(context.Background(), slog.Default())
	defer stopSignals()
	<-shutdownCtx.Done()
	slog.Info("HTTPNODE.PROCESS.STOPPING")
	cancelSweep()
	httpnode.GracefulStopWithDeadline(grpcSrv, grpcShutdownGracePeriod)
	ctx, cancel := context.WithTimeout(context.Background(), serverkit.BundledServiceGrace)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
