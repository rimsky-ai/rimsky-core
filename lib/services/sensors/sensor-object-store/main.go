// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"
)

type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }

func main() {
	host := envOr("RIMSKY_SENSOR_OBJECT_STORE_HOST", "0.0.0.0")
	port, err := agentport.Resolve("RIMSKY_SENSOR_OBJECT_STORE_PORT", 9083)
	if err != nil {
		slog.Error("sensor-object-store port", "error", err.Error())
		os.Exit(1)
	}
	rimskyEndpoint := envOr("RIMSKY_CONTROL_API_URL", "http://localhost:8080")

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.Info("sensor-object-store starting",
		"grpc_port", port,
		"rimsky_endpoint", rimskyEndpoint)

	svc := NewSensorService(rimskyEndpoint, slogAdapter{l: slog.Default()})

	svc.SetBackend("memory", NewMemoryLister())

	// @story: sensor-object-store
	if fsRoot := os.Getenv("RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT"); fsRoot != "" {
		svc.SetBackend("filesystem", NewFilesystemLister(fsRoot))
		slog.Info("sensor-object-store filesystem backend registered", "root", fsRoot)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("sensor-object-store stopping")
		cancel()
	}()
	serverkit.RunGRPC(ctx, srv, lis, "sensor-object-store")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
