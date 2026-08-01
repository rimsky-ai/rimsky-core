// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestClaimProducerObservability_Capabilities_NoObservability(t *testing.T) {
	s := NewObservabilityServer()
	caps, err := s.Capabilities(context.Background(), &genv1.GetClaimProducerCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.GetSupportsClaimGet() || caps.GetSupportsClaimStream() || caps.GetSupportsListClaims() {
		t.Fatalf("expected all supports_* false; got %+v", caps)
	}
}

func TestClaimProducerObservability_GetClaim_Unimplemented(t *testing.T) {
	s := NewObservabilityServer()
	_, err := s.GetClaim(context.Background(), &genv1.GetClaimRequest{ClaimId: "x"})
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Fatalf("err code = %v, want Unimplemented", st.Code())
	}
}
