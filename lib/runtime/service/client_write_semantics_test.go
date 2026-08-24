// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type narrowingTestClaimProducerServer struct {
	genv1.UnimplementedClaimProducerServer
	realized genv1.WriteSemantics
}

func (s *narrowingTestClaimProducerServer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
		},
	}, nil
}

func (s *narrowingTestClaimProducerServer) Open(context.Context, *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
			RealizedWriteSemantics: s.realized,
		}},
	}, nil
}

func startNarrowingTestServer(t *testing.T, realized genv1.WriteSemantics) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, &narrowingTestClaimProducerServer{realized: realized})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestClientOpen_RejectsRealizedValueOutsideOperatorDeclaredNarrowing(t *testing.T) {
	addr := startNarrowingTestServer(t, genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC)
	client, err := Dial(context.Background(), "narrow-test", addr, TLSModeOff)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	if err := client.ValidateCapabilities(claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}); err != nil {
		t.Fatalf("ValidateCapabilities: %v", err)
	}

	_, err = client.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{Intent: claimproducer.IntentReadWrite})
	if err == nil {
		t.Fatal("expected Open to reject a realized value (staged_async) outside the operator-declared narrowing (sync-only), even though it is within the producer's full advertised envelope")
	}
	if !strings.Contains(err.Error(), "operator-declared") {
		t.Fatalf("expected error to name the operator-declared envelope, got: %v", err)
	}
}

func TestClientOpen_AcceptsRealizedValueWithinOperatorDeclaredNarrowing(t *testing.T) {
	addr := startNarrowingTestServer(t, genv1.WriteSemantics_WRITE_SEMANTICS_SYNC)
	client, err := Dial(context.Background(), "narrow-test-ok", addr, TLSModeOff)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	if err := client.ValidateCapabilities(claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}); err != nil {
		t.Fatalf("ValidateCapabilities: %v", err)
	}

	out, err := client.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{Intent: claimproducer.IntentReadWrite})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if out.Result.RealizedWriteSemantics != claimproducer.WriteSemanticsSync {
		t.Fatalf("realized = %q, want sync", out.Result.RealizedWriteSemantics)
	}
}

func TestClientOpen_NoDeclaredNarrowingAllowsFullAdvertisedEnvelope(t *testing.T) {
	addr := startNarrowingTestServer(t, genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC)
	client, err := Dial(context.Background(), "narrow-test-undeclared", addr, TLSModeOff)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	out, err := client.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{Intent: claimproducer.IntentReadWrite})
	if err != nil {
		t.Fatalf("Open without a declared narrowing must still accept any producer-advertised value: %v", err)
	}
	if out.Result.RealizedWriteSemantics != claimproducer.WriteSemanticsStagedAsync {
		t.Fatalf("realized = %q, want staged_async", out.Result.RealizedWriteSemantics)
	}
}
