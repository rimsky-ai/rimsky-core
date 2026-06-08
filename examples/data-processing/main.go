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

// main stands up the DataProcessing service as a gRPC server. The supervisor
// dials this address at fan-out leaf dispatch time (configured operator-side;
// the service does not self-register) to drive the candidate lifecycle.
func main() {
	lis, err := serverkit.Listen("0.0.0.0", 9500)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	genv1.RegisterDataProcessingServer(srv, newDataProcessing())

	// Serve until SIGINT/SIGTERM, then drain in-flight RPCs gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverkit.RunGRPC(ctx, srv, lis, "example-data-processing")
}
