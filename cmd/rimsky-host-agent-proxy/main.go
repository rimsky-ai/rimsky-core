// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent-proxy
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

func main() {
	cfg := LoadConfig()

	logger := serverkit.NewJSONLoggerForLevel(cfg.LogLevel)
	slog.SetDefault(logger)
	slog.Info("rimsky-host-agent-proxy starting", "grpc_port", cfg.GRPCPort)

	state := newProxyState()

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

	// @concept: peer-auth
	// @decision: host-agent-proxy-tls
	agentTLS, err := proxyServerCredentials(cfg, identity, time.Now)
	if err != nil {
		slog.Error("proxy TLS config invalid", "error", err)
		os.Exit(1)
	}
	var agentCreds []grpc.ServerOption
	if agentTLS.Credentials != nil {
		agentCreds = append(agentCreds, grpc.Creds(agentTLS.Credentials))
		slog.Info("agent-facing TLS enabled", "source", agentTLS.Source)
	} else {
		slog.Warn("agent-facing hop runs in plaintext; the user api-key crosses it unencrypted", "source", agentTLS.Source)
	}
	publishLocalCARoot(cfg, agentTLS.LocalCAPEM)

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

	peerLis, lerr := net.Listen("tcp", fmt.Sprintf(":%d", cfg.PeerGRPCPort))
	if lerr != nil {
		slog.Error("listen failed", "error", lerr, "peer_grpc_port", cfg.PeerGRPCPort)
		os.Exit(1)
	}
	slog.Info("peer-facing listener enabled", "peer_grpc_port", cfg.PeerGRPCPort, "mtls", identity.Enabled())
	go func() {
		if serveErr := servers.peer.Serve(peerLis); serveErr != nil {
			slog.Error("peer-facing grpc serve stopped", "error", serveErr)
		}
	}()

	// @decision: graceful-shutdown
	shutdownCtx, stopSignals := serverkit.ShutdownContext(context.Background(), logger)
	defer stopSignals()
	<-shutdownCtx.Done()
	slog.Info("rimsky-host-agent-proxy shutting down")
	serverkit.GracefulStop(servers.agent, serverkit.DeployedCoreGrace)
	serverkit.GracefulStop(servers.peer, serverkit.DeployedCoreGrace)
}

// @decision: host-agent-proxy-tls
func publishLocalCARoot(cfg Config, caPEM []byte) {
	if len(caPEM) == 0 {
		return
	}
	slog.Info("the proxy serves the agent-facing hop under a CA root of its own; pin it on the agent with RIMSKY_AGENT_TLS_CA",
		"ca_root_pem", string(caPEM))
	if cfg.LocalCAPath == "" {
		return
	}
	slog.Info("the proxy keeps its agent-facing CA root", "path", cfg.LocalCAPath, "key_path", localCAKeyPath(cfg.LocalCAPath))
}

// @decision: host-agent-proxy-tls
func replaceFileAtomically(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create the temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flush the temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close the temp file: %w", err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("set the file mode: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("move the file into place: %w", err)
	}
	return nil
}

// @decision: host-agent-proxy-tls
func dropFile(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("the proxy could not drop a superseded file; an agent that pins it fails every dial",
			"path", path, "error", err)
	}
}
