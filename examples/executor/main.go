// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

// main stands up the executor as a gRPC server. The supervisor dials this
// address at dispatch time (it is configured operator-side; the executor does
// not self-register). serverkit provides only generic gRPC lifecycle helpers —
// there is no executor-specific helper, so registration is a plain
// RegisterExecutorServer call against the wire contract.
func main() {
	lis, err := serverkit.Listen("0.0.0.0", 9300)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	exec := &Executor{}
	genv1.RegisterExecutorServer(srv, exec)
	genv1.RegisterExecutorObservabilityServer(srv, exec)

	// Serve until SIGINT/SIGTERM, then drain in-flight RPCs gracefully.
	// RunGRPC blocks until ctx is cancelled.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverkit.RunGRPC(ctx, srv, lis, "example-executor")
}
