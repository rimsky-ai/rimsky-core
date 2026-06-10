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
//
// The binary also registers a minimal ClaimProducer companion so the
// Capabilities handshake can advertise the `validation` mix-in alongside
// the primary protocol — see producer.go for the rationale. A service
// author copying this example would replace the producer with their own
// primary-protocol implementation and merge the Validation server into it.
func main() {
	lis, err := serverkit.Listen("0.0.0.0", 9400)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	genv1.RegisterValidationServer(srv, newValidation())
	genv1.RegisterClaimProducerServer(srv, newProducer())

	// Serve until SIGINT/SIGTERM, then drain in-flight RPCs gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverkit.RunGRPC(ctx, srv, lis, "example-validation")
}
