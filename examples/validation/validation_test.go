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

// TestValidate_AcceptsWellFormedAndRejectsBadContext starts the example
// Validation service in-process over gRPC and asserts the protocol's two
// observable outcomes for the executor role:
//
//   - a well-formed executor context (a valid JSON-schema in attributes_schema)
//     returns valid=true with no errors;
//   - a deliberately-bad executor context (a non-JSON attributes_schema blob)
//     returns valid=false with at least one ValidationFinding carrying a
//     non-empty class, message, and path.
//
// The service routes on the ValidateRequest.context oneof — the executor arm —
// so this exercises the real per-role dispatch a copied service must implement.
func TestValidate_AcceptsWellFormedAndRejectsBadContext(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterValidationServer(srv, newValidation())
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
	client := genv1.NewValidationClient(conn)

	// Accept case: a well-formed executor context. A real JSON-schema object in
	// attributes_schema is the canonical good shape; the service must accept it.
	accept, err := client.Validate(ctx, &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{
			Executor: &genv1.ExecutorContext{
				NodeAlias:        "n1",
				AttributesSchema: []byte(`{"type":"object"}`),
				ClaimAliases:     []string{"a"},
			},
		},
	})
	if err != nil {
		t.Fatalf("validate (accept case): %v", err)
	}
	if !accept.GetValid() {
		t.Fatalf("well-formed executor context: want valid=true, got valid=false errors=%+v",
			accept.GetErrors())
	}
	if len(accept.GetErrors()) != 0 {
		t.Fatalf("well-formed executor context: want no errors, got %+v", accept.GetErrors())
	}

	// Reject case: a deliberately-bad executor context — attributes_schema is not
	// valid JSON, so the service must reject the registration.
	reject, err := client.Validate(ctx, &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{
			Executor: &genv1.ExecutorContext{
				NodeAlias:        "n2",
				AttributesSchema: []byte("this is not json {"),
				ClaimAliases:     []string{"a"},
			},
		},
	})
	if err != nil {
		t.Fatalf("validate (reject case): %v", err)
	}
	if reject.GetValid() {
		t.Fatal("bad executor context: want valid=false, got valid=true")
	}
	if len(reject.GetErrors()) == 0 {
		t.Fatal("bad executor context: want at least one ValidationFinding, got none")
	}
	f := reject.GetErrors()[0]
	if f.GetClass() == "" || f.GetMessage() == "" || f.GetPath() == "" {
		t.Fatalf("ValidationFinding missing fields: class=%q message=%q path=%q",
			f.GetClass(), f.GetMessage(), f.GetPath())
	}
}
