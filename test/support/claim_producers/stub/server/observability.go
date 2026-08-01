// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type ObservabilityServer struct {
	genv1.UnimplementedClaimProducerObservabilityServer
}

func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

func (*ObservabilityServer) Capabilities(_ context.Context, _ *genv1.GetClaimProducerCapabilitiesRequest) (*genv1.ClaimProducerObservabilityCapabilities, error) {
	return &genv1.ClaimProducerObservabilityCapabilities{
		SupportsClaimGet:              false,
		SupportsClaimStream:           false,
		SupportsListClaims:            false,
		RetentionAfterTerminalSeconds: 0,
	}, nil
}

func (*ObservabilityServer) GetClaim(_ context.Context, _ *genv1.GetClaimRequest) (*genv1.ClaimDetail, error) {
	return nil, status.Error(codes.Unimplemented, "stub store: GetClaim not supported")
}

func (*ObservabilityServer) StreamClaim(_ *genv1.StreamClaimRequest, _ genv1.ClaimProducerObservability_StreamClaimServer) error {
	return status.Error(codes.Unimplemented, "stub store: StreamClaim not supported")
}

func (*ObservabilityServer) ListClaims(_ context.Context, _ *genv1.ListClaimsRequest) (*genv1.ClaimList, error) {
	return nil, status.Error(codes.Unimplemented, "stub store: ListClaims not supported")
}

func (*ObservabilityServer) GetAdminView(_ context.Context, _ *genv1.GetAdminViewRequest) (*genv1.AdminView, error) {
	return nil, status.Error(codes.Unimplemented, "stub store: GetAdminView not supported")
}

func RegisterObservability(srv *grpc.Server) {
	genv1.RegisterClaimProducerObservabilityServer(srv, NewObservabilityServer())
}
