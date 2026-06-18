// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package stub

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
	ExpectedAttributesSchema []byte
}

var permissiveSchema = []byte(`{"type":"object"}`)

func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

func NewObservabilityServerWithSchema(schema []byte) *ObservabilityServer {
	return &ObservabilityServer{ExpectedAttributesSchema: schema}
}

func (s *ObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	schema := s.ExpectedAttributesSchema
	if len(schema) == 0 {
		schema = permissiveSchema
	}
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema: schema,
		DeclaredTags: []string{
			"ready",
			"signal",
			"checkpoint",
			"progress",
			"completed",
		},
		DeclaredErrorClasses: []string{"stub/*"},
	}, nil
}

func (*ObservabilityServer) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

func (*ObservabilityServer) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

func RegisterObservability(srv *grpc.Server) {
	genv1.RegisterExecutorObservabilityServer(srv, NewObservabilityServer())
}

func RegisterObservabilityWithSchema(srv *grpc.Server, schema []byte) {
	genv1.RegisterExecutorObservabilityServer(srv, NewObservabilityServerWithSchema(schema))
}
