// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	httpnode "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"
)

func main() {
	ops.Setup(slog.LevelInfo)
	opts, err := httpnode.LoadOptsFromEnv()
	if err != nil {
		slog.Error("http-node config", "error", err.Error())
		os.Exit(1)
	}

	identity, err := peerauth.LoadFromEnv(context.Background(), "http-node")
	if err != nil {
		slog.Error("http-node peer-auth", "error", err.Error())
		os.Exit(1)
	}
	slog.Info("http-node starting",
		"grpc_port", opts.GRPCPort,
		"http_port", opts.HTTPPort,
		"stub_mode", opts.StubMode,
		"peer_auth_mtls", identity.Enabled(),
	)

	s := httpnode.NewServer(opts)

	grpcLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", opts.Host, opts.GRPCPort))
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer(identity.GRPCServerOptions()...)
	genv1.RegisterExecutorServer(grpcSrv, s)
	obs := httpnode.RegisterObservability(grpcSrv)
	obs.SetHTTPBridgeURL(opts.HTTPBridgeURL)
	s.SetObservability(obs)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Error("grpc serve", "error", err.Error())
		}
	}()

	httpBridgeURL := opts.HTTPBridgeURL
	mux := http.NewServeMux()
	httpnode.MountBridge(mux, s)
	httpnode.MountObservabilityBridge(mux, obs, httpBridgeURL)
	httpAddr := fmt.Sprintf("%s:%d", opts.Host, opts.HTTPPort)
	httpLis, err := net.Listen("tcp", httpAddr)
	if err != nil {
		slog.Error("http listen", "error", err.Error())
		os.Exit(1)
	}
	httpSrv := httpnode.NewBridgeServer(httpAddr, mux, identity)
	go func() {
		if err := httpnode.ServeBridge(httpSrv, httpLis, identity); err != nil && err != http.ErrServerClosed {
			slog.Error("http serve", "error", err.Error())
		}
	}()

	maintainCtx, cancelMaintain := context.WithCancel(context.Background())
	defer cancelMaintain()
	go identity.Maintain(maintainCtx, peerauth.DefaultRenewCheckInterval, func(err error) {
		slog.Error("http-node peer-auth renewal", "error", err.Error())
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("http-node stopping")
	cancelSweep()
	grpcSrv.GracefulStop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
