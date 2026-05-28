// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensor-object-store — bundled object-store sensor reference
// implementation. Polls an object-store bucket+prefix on a fixed
// interval, emitting one observation per new object.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sensors as a service kind.
//
//	@concept: sensor
//
// V1 ships the in-memory backend ("memory") wired by default for
// smoke testing. Production deployments that need S3 / GCS / Azure
// backends register the corresponding ObjectLister via SetBackend at
// startup. Keeping the SDKs out of the bundled binary keeps the
// `go.mod` budget tight and avoids LocalStack-only dev dependencies
// in the default build (per the 2026-05-15 plan's pre-resolved
// decision narrowing the S3 SDK to this binary's optional build path).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }

func main() {
	host := envOr("RIMSKY_SENSOR_OBJECT_STORE_HOST", "0.0.0.0")
	port := atoiOr("RIMSKY_SENSOR_OBJECT_STORE_PORT", 9083)
	rimskyEndpoint := envOr("RIMSKY_ENDPOINT", "http://localhost:8080")

	ops.Setup(slog.LevelInfo)
	slog.Info("sensor-object-store starting",
		"grpc_port", port,
		"rimsky_endpoint", rimskyEndpoint)

	svc := NewSensorService(rimskyEndpoint, slogAdapter{l: slog.Default()})

	// Register the in-memory backend by default — useful for smoke /
	// integration tests that exercise the protocol without provisioning
	// cloud credentials.
	svc.SetBackend("memory", NewMemoryLister())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Optional state-DB persistence. Empty env → in-memory mode.
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
	srv := grpc.NewServer()
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

func atoiOr(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
