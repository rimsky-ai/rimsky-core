// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package verifierhttp

import (
	"context"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

func (s *ObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:         false,
		SupportsTraceStream:      false,
		ExpectedAttributesSchema: SchemaBytes(),
		DeclaredTags:             DeclaredTags(),
		DeclaredErrorClasses:     DeclaredErrorClasses(),
	}, nil
}

func RegisterObservability(srv *grpc.Server) *ObservabilityServer {
	o := NewObservabilityServer()
	genv1.RegisterExecutorObservabilityServer(srv, o)
	return o
}
