// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

type httpBridgeFakeServer struct {
	genv1.UnimplementedClaimProducerServer
}

func (httpBridgeFakeServer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
	}, nil
}

func (httpBridgeFakeServer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	return &genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
		Address:                []byte(req.GetClaimId()),
		ClaimScope:             []byte(req.GetSelector()),
		RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
	}}}, nil
}

func (httpBridgeFakeServer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return &genv1.CommitResponse{VersionId: "v-" + req.GetClaimId()}, nil
}

func (httpBridgeFakeServer) Abandon(context.Context, *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (httpBridgeFakeServer) Release(context.Context, *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}

func (httpBridgeFakeServer) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	return &genv1.SplitScopeResponse{SubScopes: []*genv1.SubScopeDescriptor{
		{PartitionKey: string(req.GetPartitionRequest())},
	}}, nil
}

func startHTTPBridgeFakeServer(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	serverkit.Mount(mux, httpBridgeFakeServer{})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestHTTPBridgeClaimProducer_FullLifecycleRoundTrip(t *testing.T) {
	addr := startHTTPBridgeFakeServer(t)
	p := NewHTTPBridgeClaimProducer(addr)
	ctx := context.Background()

	caps, err := p.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Contains(claimproducer.WriteSemanticsSync) {
		t.Fatalf("Capabilities: expected sync in envelope, got %+v", caps)
	}

	outcome, err := p.Open(ctx, "claim-1", claimproducer.ClaimSpec{
		ProducerName: "conformance-target",
		Selector:     "rimsky/conformance/http-bridge",
		Intent:       claimproducer.IntentReadWrite,
		Alias:        "http-bridge",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !outcome.Available {
		t.Fatalf("Open: expected Available=true, got %+v", outcome)
	}
	if string(outcome.Result.ClaimScope) != "rimsky/conformance/http-bridge" {
		t.Fatalf("Open: unexpected ClaimScope %q", outcome.Result.ClaimScope)
	}
	if outcome.Result.RealizedWriteSemantics != claimproducer.WriteSemanticsSync {
		t.Fatalf("Open: expected sync semantics, got %q", outcome.Result.RealizedWriteSemantics)
	}

	commitResult, err := p.Commit(ctx, "claim-1", outcome.Result.ClaimScope, outcome.Result.Address, "")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if commitResult.VersionID != "v-claim-1" {
		t.Fatalf("Commit: unexpected VersionID %q", commitResult.VersionID)
	}

	if err := p.Abandon(ctx, "claim-2", nil, nil, ""); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if err := p.Release(ctx, "claim-3", nil, nil, ""); err != nil {
		t.Fatalf("Release: %v", err)
	}

	split, err := p.SplitScope(ctx, claimproducer.SplitClaimScopeRequest{
		ClaimHandleID:    "claim-1",
		PartitionRequest: []byte("by-region"),
	})
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if len(split.SubClaimScopes) != 1 || split.SubClaimScopes[0].PartitionKey != "by-region" {
		t.Fatalf("SplitScope: unexpected response %+v", split)
	}
}

func TestHTTPBridgeClaimProducer_ScopesConflictReportsUnsupported(t *testing.T) {
	addr := startHTTPBridgeFakeServer(t)
	p := NewHTTPBridgeClaimProducer(addr)
	_, err := p.ScopesConflict(context.Background(), []byte("a"), []byte("a"))
	if err != claimproducer.ErrScopesConflictUnsupported {
		t.Fatalf("ScopesConflict: expected ErrScopesConflictUnsupported (no HTTP bridge route exists), got %v", err)
	}
}

func TestHTTPBridgeClaimProducer_UsableThroughRunAgainstFakeServer(t *testing.T) {
	addr := startHTTPBridgeFakeServer(t)
	p := NewHTTPBridgeClaimProducer(addr)
	results := Run(context.Background(), p)
	row := findRow(t, results, "Capabilities")
	if row.Err != nil {
		t.Fatalf("Capabilities row: %v", row.Err)
	}
}
