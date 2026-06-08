// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-host-agent-proxy is the rimsky-stack service that fronts a fleet
// of dev-machine host-agent daemons. It is a transparent forwarder for
// EVERY rimsky service protocol on the supervisor-facing side (Executor,
// ClaimProducer, Publisher, Validation, DataProcessing — all forwarded by
// one uniform resolve→spawn→tunnel mechanism; LifecycleSubscriber in
// consumer role) and maintains long-lived bidi agent connections on the
// dev-machine-facing side via the HostAgent.Connect stream. A dispatched
// call from a supervisor is resolved to an owner's connected agent, which
// lazily spawns the named local binary and tunnels the call to it. No
// fronted protocol ships as a registered-but-Unimplemented stub.
//
// The proxy is a normal multi-protocol service from rimsky's
// perspective: no tunnel-awareness leaks into the supervisor, the
// dispatch path, the error vocabulary, or graph processing.
//
// @concept: host-agent-proxy
//
// Environment variables:
//
//	RIMSKY_PROXY_GRPC_PORT   optional; default 9090. Serves both the
//	                         agent-facing HostAgent.Connect stream and the
//	                         supervisor-facing rimsky protocols.
//	RIMSKY_CONTROL_API_URL   optional; base URL for the GET /instances/{id}
//	                         binding-cache-miss fallback.
//	RIMSKY_CONTROL_API_TOKEN optional; bearer token for that fallback.
//	RIMSKY_LOG_LEVEL         optional; debug|info|warn|error (default info).
package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func main() {
	cfg := LoadConfig()

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(cfg.LogLevel)})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	slog.Info("rimsky-host-agent-proxy starting", "grpc_port", cfg.GRPCPort)

	state := newProxyState()
	grpcSrv := grpc.NewServer()

	// Agent-facing: long-lived bidi stream per connected dev-machine agent.
	genv1.RegisterHostAgentServer(grpcSrv, newAgentServer(state))

	// Supervisor-facing: the proxy fronts these protocols for late-bound
	// dev-machine bindings.
	genv1.RegisterExecutorServer(grpcSrv, newExecutorHandler(state, cfg))
	genv1.RegisterExecutorObservabilityServer(grpcSrv, newExecutorObsHandler())
	genv1.RegisterClaimProducerServer(grpcSrv, newClaimProducerHandler(state, cfg))
	genv1.RegisterClaimProducerObservabilityServer(grpcSrv, newClaimProducerObsHandler())

	// LifecycleSubscriber consumer-role: receives OnInstanceCreated to
	// populate the binding cache and OnRunScopeTerminal to drive reap.
	genv1.RegisterLifecycleSubscriberServer(grpcSrv, newLifecycleHandler(state, cfg))

	// Publisher / Validation / DataProcessing: real transparent-forwarding
	// handlers. The proxy fronts every rimsky service protocol by one
	// uniform resolve→spawn→tunnel mechanism — none ships as an
	// Unimplemented stub. Each presents exactly the fronted service's
	// protocol and forwards the dispatch to the spawned local binary.
	genv1.RegisterPublisherServer(grpcSrv, newPublisherHandler(state, cfg))
	genv1.RegisterValidationServer(grpcSrv, newValidationHandler(state, cfg))
	genv1.RegisterDataProcessingServer(grpcSrv, newDataProcessingHandler(state, cfg))

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

// parseLogLevel maps a textual level to slog.Level (mirrors the
// supervisor entrypoint's helper of the same name).
func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
