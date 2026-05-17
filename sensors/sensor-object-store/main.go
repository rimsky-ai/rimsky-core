// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

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
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }

func main() {
	host := envOr("RIMSKY_SENSOR_OBJECT_STORE_HOST", "0.0.0.0")
	port := atoiOr("RIMSKY_SENSOR_OBJECT_STORE_PORT", 9083)
	rimskyEndpoint := envOr("RIMSKY_ENDPOINT", "http://localhost:8080")

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
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
	go svc.Run(ctx)

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	srv := grpc.NewServer()
	genv1.RegisterSensorServer(srv, svc)
	go func() {
		if err := srv.Serve(lis); err != nil {
			slog.Error("grpc serve", "error", err.Error())
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("sensor-object-store stopping")
	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-stopCtx.Done():
		srv.Stop()
	}
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
