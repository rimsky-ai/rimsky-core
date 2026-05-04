package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// ObservabilityServer is the capabilities-only StoreObservability
// implementation registered on the stub store's gRPC server. The stub
// keeps no per-claim history and declares no admin views.
type ObservabilityServer struct {
	genv1.UnimplementedStoreObservabilityServer
}

// NewObservabilityServer returns the capabilities-only stub.
func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

// GetCapabilities reports the no-observability shape.
func (*ObservabilityServer) GetCapabilities(_ context.Context, _ *genv1.GetStoreCapabilitiesRequest) (*genv1.StoreObservabilityCapabilities, error) {
	return &genv1.StoreObservabilityCapabilities{
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
func (*ObservabilityServer) StreamClaim(_ *genv1.StreamClaimRequest, _ genv1.StoreObservability_StreamClaimServer) error {
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
	genv1.RegisterStoreObservabilityServer(srv, NewObservabilityServer())
}
