// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: sensor
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
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
	host := envOr("RIMSKY_SENSOR_WEBHOOK_HOST", "0.0.0.0")
	grpcPort := atoiOr("RIMSKY_SENSOR_WEBHOOK_PORT", 9084)
	webhookPort := atoiOr("RIMSKY_SENSOR_WEBHOOK_HTTP_PORT", 9184)
	rimskyEndpoint := envOr("RIMSKY_ENDPOINT", "http://localhost:8080")

	ops.Setup(slog.LevelInfo)
	slog.Info("sensor-webhook starting",
		"grpc_port", grpcPort,
		"webhook_port", webhookPort,
		"rimsky_endpoint", rimskyEndpoint)

	router := chi.NewRouter()
	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	svc := NewSensorService(rimskyEndpoint, router, slogAdapter{l: slog.Default()})

	ctxState, cancelState := context.WithCancel(context.Background())
	defer cancelState()
	state, err := openStateDB(ctxState)
	if err != nil {
		slog.Error("open state db", "error", err.Error())
		os.Exit(1)
	}
	if state != nil {
		svc.AttachStateDB(state)
		defer func() { _ = state.Close() }()
		slog.Info("sensor-webhook state db attached")
	}

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

	lis, err := serverkit.Listen(host, grpcPort)
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer()
	genv1.RegisterPublisherServer(grpcSrv, svc)
	go serverkit.Serve(grpcSrv, lis, "sensor-webhook")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("sensor-webhook stopping")
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	_ = webhookSrv.Shutdown(stopCtx)
	serverkit.GracefulStop(grpcSrv, serverkit.DefaultStopBudget)
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
