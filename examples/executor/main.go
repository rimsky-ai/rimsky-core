// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

// defaultExecutorPort is the gRPC port the example listens on when
// EXAMPLE_EXECUTOR_PORT is unset. 9300 matches the convention the
// in-tree test stubexecutor uses, so peer-side config that already names
// the stub port works against this example without reconfiguration.
const defaultExecutorPort = 9300

// main stands up the executor as a gRPC server. The supervisor dials this
// address at dispatch time (it is configured operator-side; the executor does
// not self-register). serverkit provides only generic gRPC lifecycle helpers —
// there is no executor-specific helper, so registration is a plain
// RegisterExecutorServer call against the wire contract.
//
// The bind port is configurable via env:EXAMPLE_EXECUTOR_PORT. This lets the
// cross-stack proof (examples/executor/main_e2e_test.go) point the binary at
// an OS-assigned free port without colliding with other services on the
// development machine.
func main() {
	port := defaultExecutorPort
	if raw := os.Getenv("EXAMPLE_EXECUTOR_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			log.Fatalf("EXAMPLE_EXECUTOR_PORT=%q: %v", raw, err)
		}
		port = parsed
	}

	lis, err := serverkit.Listen("0.0.0.0", port)
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
