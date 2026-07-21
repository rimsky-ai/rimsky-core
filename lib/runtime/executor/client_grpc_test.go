// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type grpcTestExecutor struct {
	genv1.UnimplementedExecutorServer
	payloadBytes int
}

func (s *grpcTestExecutor) Execute(_ context.Context, _ *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	delta, err := structpb.NewStruct(map[string]any{
		"blob": strings.Repeat("x", s.payloadBytes),
	})
	if err != nil {
		return nil, err
	}
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			Changed:         true,
			AttributesDelta: delta,
		}},
	}, nil
}

func startGRPCTestExecutor(t *testing.T, payloadBytes int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, &grpcTestExecutor{payloadBytes: payloadBytes})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return ln.Addr().String()
}

func TestNewGRPCClient_StripsGRPCScheme(t *testing.T) {
	addr := startGRPCTestExecutor(t, 16)
	c, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: "grpc://" + addr})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	outcome, _, err := c.Execute(context.Background(), &genv1.ExecuteRequest{})
	if err != nil {
		t.Fatalf("Execute against grpc://-prefixed endpoint: %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success outcome, got %T", outcome.GetOutcome())
	}
}

func TestNewGRPCClient_RejectsNonGRPCScheme(t *testing.T) {
	_, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: "http://127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected NewGRPCClient to reject a non-grpc:// scheme immediately, got nil error")
	}
	if !strings.Contains(err.Error(), "grpc://") {
		t.Fatalf("expected error naming the required grpc:// scheme, got: %v", err)
	}
}

func TestNewGRPCClient_AcceptsOutcomeLargerThanDefaultGRPCRecvLimit(t *testing.T) {
	const payloadExceedingDefaultGRPCRecvLimit = 5 * 1024 * 1024
	addr := startGRPCTestExecutor(t, payloadExceedingDefaultGRPCRecvLimit)
	c, err := NewGRPCClient(Endpoint{Transport: "grpc", URL: addr})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	outcome, _, err := c.Execute(context.Background(), &genv1.ExecuteRequest{})
	if err != nil {
		t.Fatalf("Execute with an oversized (but within-cap) Outcome must not fail on the gRPC transport: %v", err)
	}
	delta := outcome.GetSuccess().GetAttributesDelta().AsMap()
	if got, _ := delta["blob"].(string); len(got) != payloadExceedingDefaultGRPCRecvLimit {
		t.Fatalf("blob length = %d, want %d", len(got), payloadExceedingDefaultGRPCRecvLimit)
	}
}
