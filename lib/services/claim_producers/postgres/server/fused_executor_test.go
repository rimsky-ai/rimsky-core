// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func dialFusedServer(t *testing.T, dsn string, enableExecutor bool) *grpc.ClientConn {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen grpc: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen http: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Connection:     dsn,
			WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
			EnableExecutor: enableExecutor,
		}, grpcLis, httpLis, nil)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})

	conn, err := grpc.NewClient(grpcLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func runFusedServer(t *testing.T, dsn string, enableExecutor bool) genv1.ExecutorClient {
	t.Helper()
	return genv1.NewExecutorClient(dialFusedServer(t, dsn, enableExecutor))
}

func TestRun_FusedExecutor_RegistersExecutorServiceOnSharedGRPCServer(t *testing.T) {
	pool, dsn := bootPostgresTestContainer(t)
	seedStagingTable(t, pool, "fused_pass", "items", []map[string]any{
		{"id": "a", "payload": "x"},
		{"id": "b", "payload": "y"},
	})

	execClient := runFusedServer(t, dsn, true)

	ud, _ := structpb.NewStruct(map[string]any{
		"schema": "fused_pass",
		"table":  "items",
		"checks": []any{
			map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 1}},
		},
	})
	outcome, err := execClient.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
	if err != nil {
		t.Fatalf("Execute over the shared grpc.Server started by Run(EnableExecutor:true): %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success outcome through the fused server, got %T", outcome.GetOutcome())
	}
}

func TestRun_FusedExecutor_VerifierFailureCarriesErrorClassThroughSharedGRPCServer(t *testing.T) {
	pool, dsn := bootPostgresTestContainer(t)
	seedStagingTable(t, pool, "fused_fail", "items", []map[string]any{
		{"id": "a", "payload": "x"},
	})

	execClient := runFusedServer(t, dsn, true)

	ud, _ := structpb.NewStruct(map[string]any{
		"schema": "fused_fail",
		"table":  "items",
		"checks": []any{
			map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 5}},
		},
	})
	outcome, err := execClient.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
	if err != nil {
		t.Fatalf("Execute over the shared grpc.Server: %v", err)
	}
	got := outcome.GetError().GetErrorClass()
	if got != "pg/verifier_check_failed/row_count_absolute" {
		t.Fatalf("error class = %q, want pg/verifier_check_failed/row_count_absolute", got)
	}
}

func TestRun_WithoutFusedExecutor_ExecutorServiceNotRegistered(t *testing.T) {
	_, dsn := bootPostgresTestContainer(t)

	execClient := runFusedServer(t, dsn, false)

	_, err := execClient.Execute(context.Background(), &genv1.ExecuteRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("Execute against a server started with EnableExecutor:false must fail Unimplemented (no Executor registered), got %v", err)
	}
}
