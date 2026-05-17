// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

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

// Bundled verifier executor — protocol-shape reference impl. Env vars
// follow the bundled-executor pattern (`RIMSKY_EXECUTOR_<NAME>_*`).
//
//	@concept: verifier-pattern
func main() {
	host := envOr("RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_HOST", "0.0.0.0")
	port := atoiOr("RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_PORT", 9095)
	stubMode := os.Getenv("RIMSKY_EXECUTOR_STUB_MODE") == "1"

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	slog.Info("verifier-shape-checks starting", "grpc_port", port, "stub_mode", stubMode)

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, NewServer(stubMode))
	// Validation mix-in: verifier-shape-checks advertises role="executor"
	// validation so rimsky's control-api can cross-check the resolved
	// userdata at template registration. Per M / Section M2 in the
	// 2026-05-15 data-platform-extensions plan.
	genv1.RegisterValidationServer(srv, NewValidationServer())
	go func() {
		if err := srv.Serve(lis); err != nil {
			slog.Error("grpc serve", "error", err.Error())
		}
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("verifier-shape-checks stopping")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
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
