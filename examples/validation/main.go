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

// main stands up the Validation service as a gRPC server. rimsky dials this
// address at template registration time (configured operator-side; the service
// does not self-register) and calls Validate with the role-specific context.
func main() {
	lis, err := serverkit.Listen("0.0.0.0", 9400)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	genv1.RegisterValidationServer(srv, newValidation())

	// Serve until SIGINT/SIGTERM, then drain in-flight RPCs gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverkit.RunGRPC(ctx, srv, lis, "example-validation")
}
