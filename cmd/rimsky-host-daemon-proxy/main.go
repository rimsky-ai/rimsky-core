// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy
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

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
)

func main() {
	cfg := LoadConfig()

	logger := serverkit.NewJSONLoggerForLevel(cfg.LogLevel)
	slog.SetDefault(logger)
	slog.Info("PROXY.PROCESS.STARTING", "grpc_port", cfg.GRPCPort)

	state := newProxyState()

	controlAPIClient, err := controlAPIHTTPClient(cfg, 10*time.Second)
	if err != nil {
		slog.Error("PROXY.CONTROLAPICLIENT.INVALID", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	identity, err := serviceauth.LoadFromEnv(ctx, "host-daemon-proxy")
	if err != nil {
		slog.Error("PROXY.SERVICEAUTH.ENROLLFAILED", "error", err)
		os.Exit(1)
	}
	identity.StartMaintain(ctx, "host-daemon-proxy")

	// @concept: service-auth
	// @decision: host-daemon-proxy-tls
	daemonTLS, err := proxyServerCredentials(cfg, identity, time.Now)
	if err != nil {
		slog.Error("PROXY.TLSCONFIG.INVALID", "error", err)
		os.Exit(1)
	}
	var daemonCreds []grpc.ServerOption
	if daemonTLS.Credentials != nil {
		daemonCreds = append(daemonCreds, grpc.Creds(daemonTLS.Credentials))
		slog.Info("PROXY.DAEMONFACINGTLS.ENABLED", "source", daemonTLS.Source)
	} else {
		slog.Warn("PROXY.DAEMONFACINGTLS.DISABLED", "detail", "the daemon-facing hop runs in plaintext and the user api-key crosses it unencrypted", "source", daemonTLS.Source)
	}
	publishLocalCARoot(cfg, daemonTLS.LocalCAPEM)

	servers := buildProxyServers(cfg, state, identity, controlAPIClient, daemonCreds)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		slog.Error("PROXY.DAEMONFACINGLISTENER.LISTENFAILED", "error", err, "grpc_port", cfg.GRPCPort)
		os.Exit(1)
	}
	go func() {
		if serveErr := servers.daemon.Serve(lis); serveErr != nil {
			slog.Error("PROXY.DAEMONFACINGLISTENER.SERVESTOPPED", "error", serveErr)
		}
	}()

	serviceLis, lerr := net.Listen("tcp", fmt.Sprintf(":%d", cfg.ServiceGRPCPort))
	if lerr != nil {
		slog.Error("PROXY.SERVICEFACINGLISTENER.LISTENFAILED", "error", lerr, "service_grpc_port", cfg.ServiceGRPCPort)
		os.Exit(1)
	}
	slog.Info("PROXY.SERVICEFACINGLISTENER.ENABLED", "service_grpc_port", cfg.ServiceGRPCPort, "mtls", identity.Enabled())
	go func() {
		if serveErr := servers.service.Serve(serviceLis); serveErr != nil {
			slog.Error("PROXY.SERVICEFACINGLISTENER.SERVESTOPPED", "error", serveErr)
		}
	}()

	// @decision: graceful-shutdown
	shutdownCtx, stopSignals := serverkit.ShutdownContext(context.Background(), logger)
	defer stopSignals()
	<-shutdownCtx.Done()
	slog.Info("PROXY.PROCESS.STOPPING")
	serverkit.GracefulStop(servers.daemon, serverkit.DeployedCoreGrace)
	serverkit.GracefulStop(servers.service, serverkit.DeployedCoreGrace)
}

// @decision: host-daemon-proxy-tls
func publishLocalCARoot(cfg Config, caPEM []byte) {
	if len(caPEM) == 0 {
		return
	}
	slog.Info("PROXY.LOCALCA.SERVING", "detail", "the proxy serves the daemon-facing hop under a CA root of its own; pin it on the daemon with RIMSKY_DAEMON_TLS_CA",
		"ca_root_pem", string(caPEM))
	if cfg.LocalCAPath == "" {
		return
	}
	slog.Info("PROXY.LOCALCA.PERSISTED", "path", cfg.LocalCAPath, "key_path", localCAKeyPath(cfg.LocalCAPath))
}

// @decision: host-daemon-proxy-tls
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

// @decision: host-daemon-proxy-tls
func dropFile(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("PROXY.SUPERSEDEDFILE.KEPT", "detail", "the proxy could not drop a superseded file; a daemon that pins it fails every dial",
			"path", path, "error", err)
	}
}
