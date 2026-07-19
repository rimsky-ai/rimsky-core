// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type alwaysSuccessExecutor struct {
	genv1.UnimplementedExecutorServer
}

func (alwaysSuccessExecutor) Execute(context.Context, *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		ChangeSummary: "client_test: ok",
	}}}, nil
}

func startExecutorForDialing(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, alwaysSuccessExecutor{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestNewGRPCClient_AcceptsGRPCSchemePrefixedEndpoint(t *testing.T) {
	addr := startExecutorForDialing(t)
	client, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: "grpc://" + addr})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer client.Close()

	outcome, err := client.Execute(context.Background(), &genv1.ExecuteRequest{})
	if err != nil {
		t.Fatalf("Execute against a grpc://-prefixed endpoint must dial successfully (the endpoint format every other conformance subcommand accepts): %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success, got %T", outcome.GetOutcome())
	}
}

func TestNewGRPCClient_AcceptsBareHostPortEndpoint(t *testing.T) {
	addr := startExecutorForDialing(t)
	client, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: addr})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer client.Close()

	if _, err := client.Execute(context.Background(), &genv1.ExecuteRequest{}); err != nil {
		t.Fatalf("Execute against a bare host:port endpoint: %v", err)
	}
}
