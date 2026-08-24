// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/daemonport"
)

type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }

// @decision: default-port-allocation
const (
	defaultGRPCPort = 9084
	defaultHTTPPort = 9184
)

func main() {
	host := envOr("RIMSKY_SENSOR_WEBHOOK_HOST", "0.0.0.0")
	// @concept: service
	grpcPort, err := daemonport.Resolve("RIMSKY_SENSOR_WEBHOOK_PORT", defaultGRPCPort)
	if err != nil {
		slog.Error("SENSORWEBHOOK.CONFIG.INVALID", "error", err.Error())
		os.Exit(1)
	}
	webhookPort := atoiOr("RIMSKY_SENSOR_WEBHOOK_HTTP_PORT", defaultHTTPPort)
	rimskyEndpoint := envOr("RIMSKY_CONTROL_API_URL", "http://localhost:8080")

	slog.SetDefault(serverkit.NewJSONLogger())
	slog.Info("SENSORWEBHOOK.PROCESS.STARTING",
		"grpc_port", grpcPort,
		"webhook_port", webhookPort,
		"rimsky_endpoint", rimskyEndpoint)

	router := chi.NewRouter()
	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	svc := NewSensorService(rimskyEndpoint, router, slogAdapter{l: slog.Default()})

	// @decision: graceful-shutdown
	ctxState, stopSignals := serverkit.ShutdownContext(context.Background(), slog.Default())
	defer stopSignals()

	identity, err := serviceauth.LoadFromEnv(ctxState, "sensor-webhook")
	if err != nil {
		slog.Error("SENSORWEBHOOK.SERVICEAUTH.ENROLLFAILED", "error", err.Error())
		os.Exit(1)
	}
	svc.SetPublishClient(identity.OutboundHTTPClient(10 * time.Second))
	identity.StartMaintain(ctxState, "sensor-webhook")

	state, err := openStateDB(ctxState)
	if err != nil {
		slog.Error("SENSORWEBHOOK.STATEDB.OPENFAILED", "error", err.Error())
		os.Exit(1)
	}
	if state != nil {
		svc.AttachStateDB(state)
		defer func() { _ = state.Close() }()
		slog.Info("SENSORWEBHOOK.STATEDB.ATTACHED")
	}

	webhookSrv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, webhookPort),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := webhookSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("SENSORWEBHOOK.HTTP.SERVEFAILED", "error", err.Error())
		}
	}()

	lis, err := serverkit.Listen(host, grpcPort)
	if err != nil {
		slog.Error("SENSORWEBHOOK.GRPC.LISTENFAILED", "error", err.Error())
		os.Exit(1)
	}
	grpcSrv := identity.GRPCServer()
	genv1.RegisterPublisherServer(grpcSrv, svc)
	go serverkit.Serve(grpcSrv, lis, "sensor-webhook")

	<-ctxState.Done()
	slog.Info("SENSORWEBHOOK.PROCESS.STOPPING")
	stopCtx, stopCancel := context.WithTimeout(context.Background(), serverkit.BundledServiceGrace)
	defer stopCancel()
	_ = webhookSrv.Shutdown(stopCtx)
	serverkit.GracefulStop(grpcSrv, serverkit.BundledServiceGrace)
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
