// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// sensor-webhook — bundled webhook sensor reference implementation.
// Runs an HTTP server on the configured port; each watch registers a
// route under its `path_prefix`. Inbound POSTs push observations to
// rimsky.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sensors as a service kind.
//
//	@concept: sensor
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }

func main() {
	host := envOr("RIMSKY_SENSOR_WEBHOOK_HOST", "0.0.0.0")
	grpcPort := atoiOr("RIMSKY_SENSOR_WEBHOOK_PORT", 9084)
	webhookPort := atoiOr("RIMSKY_SENSOR_WEBHOOK_HTTP_PORT", 9184)
	rimskyEndpoint := envOr("RIMSKY_ENDPOINT", "http://localhost:8080")

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	slog.Info("sensor-webhook starting",
		"grpc_port", grpcPort,
		"webhook_port", webhookPort,
		"rimsky_endpoint", rimskyEndpoint)

	router := chi.NewRouter()
	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	svc := NewSensorService(rimskyEndpoint, router, slogAdapter{l: slog.Default()})

	// Inbound-webhook HTTP server. Distinct from the gRPC port so
	// operator routing can expose the webhook surface publicly while
	// keeping the gRPC port private.
	webhookSrv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, webhookPort),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := webhookSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("webhook server failed", "error", err.Error())
		}
	}()

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, grpcPort))
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer()
	genv1.RegisterSensorServer(grpcSrv, svc)
	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			slog.Error("grpc serve", "error", err.Error())
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("sensor-webhook stopping")
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	_ = webhookSrv.Shutdown(stopCtx)
	stopped := make(chan struct{})
	go func() {
		grpcSrv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-stopCtx.Done():
		grpcSrv.Stop()
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
