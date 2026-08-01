// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"
)

type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }

func main() {
	host := envOr("RIMSKY_SENSOR_HTTP_HOST", "0.0.0.0")
	port, err := agentport.Resolve("RIMSKY_SENSOR_HTTP_PORT", 9082)
	if err != nil {
		slog.Error("sensor-http port", "error", err.Error())
		os.Exit(1)
	}
	rimskyEndpoint := envOr("RIMSKY_CONTROL_API_URL", "http://localhost:8080")

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.Info("sensor-http starting",
		"grpc_port", port,
		"rimsky_endpoint", rimskyEndpoint)

	pollGuard, err := egress.NewGuardFromEnv("RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST")
	if err != nil {
		slog.Error("sensor-http egress allowlist", "error", err.Error())
		os.Exit(1)
	}

	svc := NewSensorService(rimskyEndpoint, pollGuard, slogAdapter{l: slog.Default()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	identity, err := peerauth.LoadFromEnv(ctx, "sensor-http")
	if err != nil {
		slog.Error("sensor-http peer-auth", "error", err.Error())
		os.Exit(1)
	}
	svc.SetPublishClient(identity.OutboundHTTPClient(30 * time.Second))
	identity.StartMaintain(ctx, "sensor-http")

	state, err := openStateDB(ctx)
	if err != nil {
		slog.Error("open state db", "error", err.Error())
		os.Exit(1)
	}
	if state != nil {
		svc.AttachStateDB(state)
		defer func() { _ = state.Close() }()
		slog.Info("sensor-http state db attached")
	}

	go svc.Run(ctx)

	lis, err := serverkit.Listen(host, port)
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	srv := identity.GRPCServer()
	genv1.RegisterPublisherServer(srv, svc)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("sensor-http stopping")
		cancel()
	}()
	serverkit.RunGRPC(ctx, srv, lis, "sensor-http")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
