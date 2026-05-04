package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

func TestStoreObservability_Capabilities_NoObservability(t *testing.T) {
	s := NewObservabilityServer()
	caps, err := s.GetCapabilities(context.Background(), &genv1.GetStoreCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if caps.GetSupportsClaimGet() || caps.GetSupportsClaimStream() || caps.GetSupportsListClaims() {
		t.Fatalf("expected all supports_* false; got %+v", caps)
	}
}

func TestStoreObservability_GetClaim_Unimplemented(t *testing.T) {
	s := NewObservabilityServer()
	_, err := s.GetClaim(context.Background(), &genv1.GetClaimRequest{ClaimId: "x"})
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Fatalf("err code = %v, want Unimplemented", st.Code())
	}
}
