// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// lifecycle.go provides the generic gRPC server bring-up + graceful-
// shutdown shape that every bundled and third-party rimsky-implementing
// service uses. The caller registers its protocol servers on the
// returned *grpc.Server before calling Serve. The helper handles
// the listen/serve/graceful-stop pattern that was previously copy-
// pasted across every sensor / subscriber / executor main.go.

package serverkit

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
)

// DefaultStopBudget bounds GracefulStop() so a hung in-flight RPC
// can't strand a service when shutdown is requested.
const DefaultStopBudget = 10 * time.Second

// Listen opens a TCP listener at host:port. Returns a clear error
// (with the bind address embedded) so log output is unambiguous
// about which port failed.
func Listen(host string, port int) (net.Listener, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return lis, nil
}

// Serve runs srv.Serve(lis) in the calling goroutine; intended to
// be invoked from a goroutine by the caller. Logs at slog.Warn if
// Serve returns a non-nil error (typically the listener being
// closed during shutdown). Returns nothing — Serve never returns
// before shutdown.
func Serve(srv *grpc.Server, lis net.Listener, serviceName string) {
	if err := srv.Serve(lis); err != nil {
		slog.Warn(serviceName+": grpc serve", "error", err.Error())
	}
}

// GracefulStop runs srv.GracefulStop() with a wall-clock budget so a
// stuck in-flight RPC cannot strand shutdown. After budget elapses,
// srv.Stop() is invoked to force-close. Returns when both have run
// to completion.
//
// Pass budget=0 to use DefaultStopBudget.
func GracefulStop(srv *grpc.Server, budget time.Duration) {
	if budget == 0 {
		budget = DefaultStopBudget
	}
	stopTimer := time.AfterFunc(budget, srv.Stop)
	srv.GracefulStop()
	stopTimer.Stop()
}

// RunGRPC is the all-in-one bring-up + serve + shutdown wrapper. It
// returns after ctx is cancelled, having served on lis throughout
// and gracefully stopped on shutdown. The caller registers
// protocol servers on the returned *grpc.Server BEFORE the goroutine
// dispatch; RunGRPC blocks until ctx is cancelled.
//
// Useful for services that want the lifecycle handled in one call.
// Services with multiple listeners (gRPC + HTTP + admin) should
// stitch Listen/Serve/GracefulStop together themselves.
func RunGRPC(ctx context.Context, srv *grpc.Server, lis net.Listener, serviceName string) {
	go Serve(srv, lis, serviceName)
	<-ctx.Done()
	GracefulStop(srv, DefaultStopBudget)
}
