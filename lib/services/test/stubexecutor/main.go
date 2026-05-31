// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Command stubexecutor is a test-only Executor that returns Success for every
// dispatch. The integration harness builds it on demand (testcontainers
// FromDockerfile) and registers it as a peer executor so tests about stores,
// subscribers, and observability can complete the claim loop without a real
// executor. It is never published as a product image.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

// server implements genv1.ExecutorServer with a single terminal Success.
type server struct {
	genv1.UnimplementedExecutorServer
}

// Execute emits exactly one StreamClose/Success event and closes the stream,
// per the Executor contract (zero heartbeats, no attribute writeback).
func (server) Execute(_ *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			Changed:       false,
			ChangeSummary: "stub executor: success",
		}}},
	}})
}

// observability implements genv1.ExecutorObservabilityServer. The standalone
// stub keeps no traces, but it MUST answer Capabilities with a non-nil
// expected-attributes schema: the dispatch-time attribute-surface gate
// (runtime.resolveAttributes) rejects any attribute-bearing node whose
// executor advertises no schema with executor_schema_unavailable. Advertising
// the permissive open shape `{"type":"object"}` (no `properties` block, which
// graph/node.IsPermissiveExecutorSchema reads as "open shape") lets stub nodes
// that carry an `attributes:` block dispatch and settle instead of failing.
//
// This mirrors test/support/executors/stub's observability server, duplicated
// here because the lib/services module requires only lib/protocols and may not
// import the root module's test-support packages (consumption-side-isolation).
type observability struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// Capabilities reports the no-observability shape with a permissive
// expected-attributes schema.
func (observability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}, nil
}

// GetTrace returns Unimplemented; the stub retains no traces.
func (observability) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

// StreamTrace returns Unimplemented.
func (observability) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

func main() {
	ops.Setup(slog.LevelInfo)
	bind := os.Getenv("EXECUTOR_STUB_BIND")
	if bind == "" {
		bind = "0.0.0.0:9300"
	}
	lis, err := net.Listen("tcp", bind)
	if err != nil {
		slog.Error("stubexecutor listen", "error", err.Error(), "bind", bind)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, server{})
	genv1.RegisterExecutorObservabilityServer(srv, observability{})
	slog.Info("stubexecutor listening", "bind", bind)
	if err := srv.Serve(lis); err != nil {
		slog.Error("stubexecutor serve", "error", err.Error())
		os.Exit(1)
	}
}
