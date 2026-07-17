// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"
)

func main() {
	ops.Setup(slog.LevelInfo)
	opts, err := claudeagent.LoadOptsFromEnv()
	if err != nil {
		slog.Error("claude-agent config", "error", err.Error())
		os.Exit(1)
	}
	if !opts.CredentialsConfigured() {
		slog.Error("claude-agent config", "error", claudeagent.ErrCredentialsMissing.Error())
		os.Exit(1)
	}

	authMode := "stub"
	if opts.Auth.AnthropicAPIKey != "" {
		authMode = "api_key"
	} else if opts.Auth.ClaudeCodeOauthToken != "" {
		authMode = "oauth"
	}
	slog.Info("claude-agent starting",
		"grpc_port", opts.GrpcPort,
		"http_port", opts.HTTPPort,
		"stub_mode", opts.StubMode,
		"auth_mode", authMode,
		"mcp_allowlist_open", opts.McpAllowlist.Open(),
		"expose_env_allowlist_open", opts.ExposeEnvAllowlist.Open(),
	)

	identity, err := peerauth.LoadFromEnv(context.Background(), "claude-agent")
	if err != nil {
		slog.Error("claude-agent peer-auth", "error", err.Error())
		os.Exit(1)
	}
	callbackClient := http.DefaultClient
	if identity.Enabled() {
		callbackClient = &http.Client{Transport: &http.Transport{TLSClientConfig: identity.ClientTLSConfig()}}
	}

	obs := claudeagent.NewObservabilityServer(opts.ObservabilityHTTPBridgeURL)
	executor := claudeagent.NewExecutorServer(claudeagent.ServerConfig{
		Opts:          opts,
		Observability: obs,
		Logger:        slog.Default(),
		PostCallback:  claudeagent.PostCallbackVia(callbackClient),
	})

	grpcSrv, err := claudeagent.StartGrpcServer(opts.Host, opts.GrpcPort, executor, obs, identity)
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	slog.Info("claude-agent gRPC server listening", "addr", grpcSrv.Address, "peer_auth_mtls", identity.Enabled())

	httpBridge, err := claudeagent.StartHTTPBridge(opts.Host, opts.HTTPPort, executor, identity)
	if err != nil {
		slog.Error("http bridge listen", "error", err.Error())
		os.Exit(1)
	}
	slog.Info("claude-agent HTTP bridge listening", "addr", httpBridge.Address)

	sweepCtx, cancelSweep := context.WithCancel(context.Background())
	defer cancelSweep()
	identity.StartMaintain(sweepCtx, "claude-agent")
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
	slog.Info("claude-agent stopping")
	cancelSweep()
	grpcSrv.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpBridge.Shutdown(ctx)
}
