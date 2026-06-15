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
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

func main() {
	cfg := LoadConfig()
	ops.Setup(slog.LevelInfo)
	slog.Info("http-node starting",
		"grpc_port", cfg.GRPCPort,
		"http_port", cfg.HTTPPort,
		"stub_mode", cfg.StubMode,
	)

	s := NewServer(cfg)

	grpcLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.GRPCPort))
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer()
	genv1.RegisterExecutorServer(grpcSrv, s)
	obs := RegisterObservability(grpcSrv)
	obs.SetHTTPBridgeURL(cfg.HTTPBridgeURL)
	s.SetObservability(obs)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Error("grpc serve", "error", err.Error())
		}
	}()

	// @deliberate: one HTTP listener hosts both the dispatch `/v1/Execute`
	// endpoint and the observability `/observability/v1/*` endpoints —
	// different path prefixes share a single port per spec §2.1.
	httpBridgeURL := cfg.HTTPBridgeURL
	mux := http.NewServeMux()
	mountBridge(mux, s)
	mountObservabilityBridge(mux, obs, httpBridgeURL)
	httpSrv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.HTTPPort),
		Handler: mux,
	}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http serve", "error", err.Error())
		}
	}()

	// @constraint: SweepEvicted must run periodically to bound observability
	// memory growth as the retention TTL passes for terminal-ed dispatches.
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
