// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// ObservabilityServer is the capabilities-only ClaimProducerObservability
// implementation registered on the stub store's gRPC server. The stub
// keeps no per-claim history and declares no admin views.
type ObservabilityServer struct {
	genv1.UnimplementedClaimProducerObservabilityServer
}

// NewObservabilityServer returns the capabilities-only stub.
func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

// Capabilities reports the no-observability shape.
func (*ObservabilityServer) Capabilities(_ context.Context, _ *genv1.GetClaimProducerCapabilitiesRequest) (*genv1.ClaimProducerObservabilityCapabilities, error) {
	return &genv1.ClaimProducerObservabilityCapabilities{
		SupportsClaimGet:              false,
		SupportsClaimStream:           false,
		SupportsListClaims:            false,
		RetentionAfterTerminalSeconds: 0,
	}, nil
}

// GetClaim returns Unimplemented.
func (*ObservabilityServer) GetClaim(_ context.Context, _ *genv1.GetClaimRequest) (*genv1.ClaimDetail, error) {
	return nil, status.Error(codes.Unimplemented, "stub store: GetClaim not supported")
}

// StreamClaim returns Unimplemented.
func (*ObservabilityServer) StreamClaim(_ *genv1.StreamClaimRequest, _ genv1.ClaimProducerObservability_StreamClaimServer) error {
	return status.Error(codes.Unimplemented, "stub store: StreamClaim not supported")
}

// ListClaims returns Unimplemented.
func (*ObservabilityServer) ListClaims(_ context.Context, _ *genv1.ListClaimsRequest) (*genv1.ClaimList, error) {
	return nil, status.Error(codes.Unimplemented, "stub store: ListClaims not supported")
}

// GetAdminView returns Unimplemented.
func (*ObservabilityServer) GetAdminView(_ context.Context, _ *genv1.GetAdminViewRequest) (*genv1.AdminView, error) {
	return nil, status.Error(codes.Unimplemented, "stub store: GetAdminView not supported")
}

// RegisterObservability registers the stub observability server on srv.
func RegisterObservability(srv *grpc.Server) {
	genv1.RegisterClaimProducerObservabilityServer(srv, NewObservabilityServer())
}
