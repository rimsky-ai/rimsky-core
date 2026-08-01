// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent-proxy
package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func main() {
	cfg := LoadConfig()

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: shared.ParseLogLevel(cfg.LogLevel)})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	slog.Info("rimsky-host-agent-proxy starting", "grpc_port", cfg.GRPCPort)

	state := newProxyState()

	creds, err := proxyServerCredentials(cfg)
	if err != nil {
		slog.Error("proxy TLS config invalid", "error", err)
		os.Exit(1)
	}
	var serverOpts []grpc.ServerOption
	if creds != nil {
		serverOpts = append(serverOpts, grpc.Creds(creds))
		slog.Info("agent-facing TLS enabled", "cert", cfg.TLSCertPath)
	}
	grpcSrv := grpc.NewServer(serverOpts...)

	verifyIdentity := newControlAPIRegisterIdentityVerifier(&http.Client{Timeout: 10 * time.Second}, cfg.ControlAPIURL)
	genv1.RegisterHostAgentServer(grpcSrv, newAgentServer(state, verifyIdentity))

	genv1.RegisterExecutorServer(grpcSrv, newExecutorHandler(state, cfg))
	genv1.RegisterExecutorObservabilityServer(grpcSrv, newExecutorObsHandler())
	genv1.RegisterClaimProducerServer(grpcSrv, newClaimProducerHandler(state, cfg))
	genv1.RegisterClaimProducerObservabilityServer(grpcSrv, newClaimProducerObsHandler())

	genv1.RegisterLifecycleSubscriberServer(grpcSrv, newLifecycleHandler(state, cfg))

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		slog.Error("listen failed", "error", err, "grpc_port", cfg.GRPCPort)
		os.Exit(1)
	}

	go func() {
		if serveErr := grpcSrv.Serve(lis); serveErr != nil {
			slog.Error("grpc serve stopped", "error", serveErr)
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	slog.Info("rimsky-host-agent-proxy shutting down")
	grpcSrv.GracefulStop()
}
