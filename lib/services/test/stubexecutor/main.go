// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
)

const forcedErrorClass = "stub/forced_error"

type server struct {
	genv1.UnimplementedExecutorServer
	forceError bool
}

func (s server) Execute(_ context.Context, _ *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	if s.forceError {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: forcedErrorClass,
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       false,
		ChangeSummary: "stub executor: success",
	}}}, nil
}

type observability struct {
	genv1.UnimplementedExecutorObservabilityServer
	forceError bool
}

func (o observability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	caps := &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}
	if o.forceError {
		caps.DeclaredErrorClasses = []string{forcedErrorClass}
	}
	return caps, nil
}

func (observability) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

func (observability) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	bind := os.Getenv("EXECUTOR_STUB_BIND")
	if bind == "" {
		bind = "0.0.0.0:9300"
	}
	lis, err := net.Listen("tcp", bind)
	if err != nil {
		slog.Error("STUBEXECUTOR.GRPC.LISTENFAILED", "error", err.Error(), "bind", bind)
		os.Exit(1)
	}
	forceError := os.Getenv("EXECUTOR_STUB_FORCE_ERROR") == "1"
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, server{forceError: forceError})
	genv1.RegisterExecutorObservabilityServer(srv, observability{forceError: forceError})
	slog.Info("STUBEXECUTOR.GRPC.LISTENING", "bind", bind, "force_error", forceError)
	if err := srv.Serve(lis); err != nil {
		slog.Error("STUBEXECUTOR.GRPC.SERVEFAILED", "error", err.Error())
		os.Exit(1)
	}
}
