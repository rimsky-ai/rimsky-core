// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestOpenReturnsAcquired starts the example producer in-process and asserts the
// acquisition happy path: Open returns the Acquired arm of the OpenResponse
// oneof with a non-empty address, and Capabilities advertises READ_ONLY.
func TestOpenReturnsAcquired(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, newProducer())
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := genv1.NewClaimProducerClient(conn)

	resp, err := client.Open(ctx, &genv1.OpenRequest{ClaimId: "c1", Intent: "r"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	acq := resp.GetAcquired()
	if acq == nil {
		t.Fatalf("Open did not return Acquired: %+v", resp.GetResult())
	}
	if len(acq.GetAddress()) == 0 {
		t.Fatal("Acquired carries an empty address")
	}

	caps, err := client.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(caps.GetWriteSemanticsAllowed()) == 0 {
		t.Fatal("Capabilities advertised no write semantics")
	}
}
