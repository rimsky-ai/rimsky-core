// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
)

type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }

// @decision: default-port-allocation
const defaultGRPCPort = 9083

func main() {
	host := envOr("RIMSKY_SENSOR_OBJECT_STORE_HOST", "0.0.0.0")
	port, err := agentport.Resolve("RIMSKY_SENSOR_OBJECT_STORE_PORT", defaultGRPCPort)
	if err != nil {
		slog.Error("sensor-object-store port", "error", err.Error())
		os.Exit(1)
	}
	rimskyEndpoint := envOr("RIMSKY_CONTROL_API_URL", "http://localhost:8080")

	slog.SetDefault(serverkit.NewJSONLogger())
	slog.Info("sensor-object-store starting",
		"grpc_port", port,
		"rimsky_endpoint", rimskyEndpoint)

	svc := NewSensorService(rimskyEndpoint, slogAdapter{l: slog.Default()})

	for _, name := range registerBackendsFromEnv(svc) {
		slog.Info("sensor-object-store backend registered", "backend", name)
	}

	// @decision: graceful-shutdown
	ctx, stopSignals := serverkit.ShutdownContext(context.Background(), slog.Default())
	defer stopSignals()

	identity, err := peerauth.LoadFromEnv(ctx, "sensor-object-store")
	if err != nil {
		slog.Error("sensor-object-store peer-auth", "error", err.Error())
		os.Exit(1)
	}
	svc.SetPublishClient(identity.OutboundHTTPClient(30 * time.Second))
	identity.StartMaintain(ctx, "sensor-object-store")

	state, err := openStateDB(ctx)
	if err != nil {
		slog.Error("open state db", "error", err.Error())
		os.Exit(1)
	}
	if state != nil {
		svc.AttachStateDB(state)
		defer func() { _ = state.Close() }()
		slog.Info("sensor-object-store state db attached")
	}

	go svc.Run(ctx)

	lis, err := serverkit.Listen(host, port)
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	srv := identity.GRPCServer()
	genv1.RegisterPublisherServer(srv, svc)

	serverkit.RunGRPC(ctx, srv, lis, "sensor-object-store")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// @decision: object-store-watching-model
// @story: sensor-object-store
func registerBackendsFromEnv(svc *SensorService) []string {
	var registered []string
	if fsRoot := os.Getenv("RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT"); fsRoot != "" {
		svc.SetBackend("filesystem", NewFilesystemLister(fsRoot))
		registered = append(registered, "filesystem")
	}
	if os.Getenv("RIMSKY_SENSOR_OBJECT_STORE_ENABLE_MEMORY_BACKEND") == "1" {
		svc.SetBackend("memory", NewMemoryLister())
		registered = append(registered, "memory")
	}
	return registered
}
