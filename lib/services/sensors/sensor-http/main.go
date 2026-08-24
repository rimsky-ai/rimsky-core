// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/daemonport"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"
)

type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }

// @decision: default-port-allocation
const defaultGRPCPort = 9082

func main() {
	host := envOr("RIMSKY_SENSOR_HTTP_HOST", "0.0.0.0")
	port, err := daemonport.Resolve("RIMSKY_SENSOR_HTTP_PORT", defaultGRPCPort)
	if err != nil {
		slog.Error("SENSORHTTP.PORT.INVALID", "error", err.Error())
		os.Exit(1)
	}
	rimskyEndpoint := envOr("RIMSKY_CONTROL_API_URL", "http://localhost:8080")

	slog.SetDefault(serverkit.NewJSONLogger())
	slog.Info("SENSORHTTP.PROCESS.STARTING",
		"grpc_port", port,
		"rimsky_endpoint", rimskyEndpoint)

	// @decision: destination-allowlists-default-closed
	pollGuard, err := egress.NewGuardFromEnv("RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST")
	if err != nil {
		slog.Error("SENSORHTTP.EGRESSALLOWLIST.INVALID", "error", err.Error())
		os.Exit(1)
	}

	svc := NewSensorService(rimskyEndpoint, pollGuard, slogAdapter{l: slog.Default()})

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), slog.Default())
	defer stopSignals()

	identity, err := serviceauth.LoadFromEnv(ctx, "sensor-http")
	if err != nil {
		slog.Error("SENSORHTTP.SERVICEAUTH.ENROLLFAILED", "error", err.Error())
		os.Exit(1)
	}
	svc.SetPublishClient(identity.OutboundHTTPClient(30 * time.Second))
	identity.StartMaintain(ctx, "sensor-http")

	state, err := openStateDB(ctx)
	if err != nil {
		slog.Error("SENSORHTTP.STATEDB.OPENFAILED", "error", err.Error())
		os.Exit(1)
	}
	if state != nil {
		svc.AttachStateDB(state)
		defer func() { _ = state.Close() }()
		slog.Info("SENSORHTTP.STATEDB.ATTACHED")
	}

	go svc.Run(ctx)

	lis, err := serverkit.Listen(host, port)
	if err != nil {
		slog.Error("SENSORHTTP.GRPC.LISTENFAILED", "error", err.Error())
		os.Exit(1)
	}
	srv := identity.GRPCServer()
	genv1.RegisterPublisherServer(srv, svc)

	serverkit.RunGRPC(ctx, srv, lis, "sensor-http")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
