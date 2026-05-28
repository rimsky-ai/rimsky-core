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
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"

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
	slog.Info("stubexecutor listening", "bind", bind)
	if err := srv.Serve(lis); err != nil {
		slog.Error("stubexecutor serve", "error", err.Error())
		os.Exit(1)
	}
}
