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
// The default bundled image always registers the in-memory backend
// ("memory") and conditionally registers the filesystem backend
// ("filesystem", when env RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT is set).
// Those are the SDK-free backends shipped here: the sensor advertises
// (Capabilities) and accepts (Subscribe) exactly the registered set,
// so a subscription naming s3/gcs/azure is rejected at Subscribe
// rather than silently no-op'ing at poll time. S3 / GCS / Azure are
// deliberately NOT implemented in this binary — keeping the cloud
// SDKs out of the default build keeps the `go.mod` budget tight and
// avoids LocalStack-only dev dependencies. A deployment that needs a
// cloud backend builds its own binary that constructs the desired
// ObjectLister, registers it via svc.SetBackend("s3", …) before
// svc.Run, and the sensor then advertises and accepts that backend
// automatically.
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

	// @deliberate: in-memory backend is the always-registered default so
	// Capabilities advertises and Subscribe accepts "memory" at minimum;
	// s3/gcs/azure stay rejected unless a production build wires them via
	// SetBackend before svc.Run.
	svc.SetBackend("memory", NewMemoryLister())

	// @story: sensor-object-store — env RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT,
	// when set, names the host- (or volume-) provided base directory the
	// lister treats as the object-store root, with buckets mapping to
	// first-level subdirectories under it. Empty env omits "filesystem"
	// from Capabilities and Subscribe rejects it; setting it makes
	// "filesystem" a first-class backend (advertised, accepted, polled
	// through the real loop) on this binary without dragging in cloud
	// SDKs. The cross-stack proof uses this path because it exhibits the
	// pluggable-backend contract end-to-end with a backend the test
	// process can mutate externally (drop a file into the mounted volume).
	if fsRoot := os.Getenv("RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT"); fsRoot != "" {
		svc.SetBackend("filesystem", NewFilesystemLister(fsRoot))
		slog.Info("sensor-object-store filesystem backend registered", "root", fsRoot)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// @deliberate: state-DB persistence is optional — empty env leaves the
	// sensor in in-memory mode rather than failing startup.
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
