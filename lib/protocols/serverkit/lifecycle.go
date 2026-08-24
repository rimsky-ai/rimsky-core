// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
)

func Listen(host string, port int) (net.Listener, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return lis, nil
}

func Serve(srv *grpc.Server, lis net.Listener, serviceName string) {
	if err := srv.Serve(lis); err != nil {
		slog.Warn("SERVERKIT.GRPC.SERVESTOPPED", "service", serviceName, "error", err.Error())
	}
}

func GracefulStop(srv *grpc.Server, budget time.Duration) {
	if budget == 0 {
		budget = BundledServiceGrace
	}
	stopTimer := time.AfterFunc(budget, srv.Stop)
	srv.GracefulStop()
	stopTimer.Stop()
}

func RunGRPC(ctx context.Context, srv *grpc.Server, lis net.Listener, serviceName string) {
	go Serve(srv, lis, serviceName)
	<-ctx.Done()
	GracefulStop(srv, BundledServiceGrace)
}
