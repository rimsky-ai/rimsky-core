// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

// @decision: graceful-shutdown
const grpcShutdownGracePeriod = serverkit.BundledServiceGrace

func Serve(opts Opts) error {
	if !opts.CredentialsConfigured() {
		return ErrCredentialsMissing
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
		return err
	}
	callbackClient := &http.Client{Timeout: callbackPostTimeout}
	if identity.Enabled() {
		callbackClient.Transport = &http.Transport{TLSClientConfig: identity.ClientTLSConfig()}
	}

	obs := NewObservabilityServer(opts.ObservabilityHTTPBridgeURL)
	executor := NewExecutorServer(ServerConfig{
		Opts:          opts,
		Observability: obs,
		Logger:        slog.Default(),
		PostCallback:  PostCallbackVia(callbackClient),
	})

	grpcSrv, err := StartGrpcServer(opts.Host, opts.GrpcPort, executor, obs, identity)
	if err != nil {
		return err
	}
	slog.Info("claude-agent gRPC server listening", "addr", grpcSrv.Address, "peer_auth_mtls", identity.Enabled())

	httpBridge, err := StartHTTPBridge(opts.Host, opts.HTTPPort, executor, identity)
	if err != nil {
		return err
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

	// @decision: graceful-shutdown
	shutdownCtx, stopSignals := serverkit.ShutdownContext(context.Background(), slog.Default())
	defer stopSignals()
	<-shutdownCtx.Done()
	slog.Info("claude-agent stopping")
	cancelSweep()
	grpcShutdownCtx, cancelGrpcShutdown := context.WithTimeout(context.Background(), grpcShutdownGracePeriod)
	defer cancelGrpcShutdown()
	grpcSrv.Shutdown(grpcShutdownCtx)
	ctx, cancel := context.WithTimeout(context.Background(), serverkit.BundledServiceGrace)
	defer cancel()
	return httpBridge.Shutdown(ctx)
}
