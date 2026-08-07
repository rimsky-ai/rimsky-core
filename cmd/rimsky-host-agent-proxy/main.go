// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent-proxy
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
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
	var agentCreds []grpc.ServerOption
	if creds != nil {
		agentCreds = append(agentCreds, grpc.Creds(creds))
		slog.Info("agent-facing TLS enabled", "cert", cfg.TLSCertPath)
	}

	controlAPIClient, err := controlAPIHTTPClient(cfg, 10*time.Second)
	if err != nil {
		slog.Error("control-API client config invalid", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	identity, err := peerauth.LoadFromEnv(ctx, "host-agent-proxy")
	if err != nil {
		slog.Error("peer-auth enrollment failed", "error", err)
		os.Exit(1)
	}
	identity.StartMaintain(ctx, "host-agent-proxy")

	servers := buildProxyServers(cfg, state, identity, controlAPIClient, agentCreds)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		slog.Error("listen failed", "error", err, "grpc_port", cfg.GRPCPort)
		os.Exit(1)
	}
	go func() {
		if serveErr := servers.agent.Serve(lis); serveErr != nil {
			slog.Error("agent-facing grpc serve stopped", "error", serveErr)
		}
	}()

	if servers.peer != nil {
		supLis, lerr := net.Listen("tcp", fmt.Sprintf(":%d", cfg.PeerGRPCPort))
		if lerr != nil {
			slog.Error("listen failed", "error", lerr, "peer_grpc_port", cfg.PeerGRPCPort)
			os.Exit(1)
		}
		slog.Info("peer-facing mTLS listener enabled", "peer_grpc_port", cfg.PeerGRPCPort)
		go func() {
			if serveErr := servers.peer.Serve(supLis); serveErr != nil {
				slog.Error("peer-facing grpc serve stopped", "error", serveErr)
			}
		}()
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	slog.Info("rimsky-host-agent-proxy shutting down")
	servers.agent.GracefulStop()
	if servers.peer != nil {
		servers.peer.GracefulStop()
	}
}
