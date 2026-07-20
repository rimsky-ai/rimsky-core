// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package serverkit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type fakeCPServer struct {
	genv1.UnimplementedClaimProducerServer

	capsResp *genv1.CapabilitiesResponse
	capsErr  error

	lastOpen *genv1.OpenRequest
	openResp *genv1.OpenResponse
	openErr  error

	lastCommit *genv1.CommitRequest
	commitResp *genv1.CommitResponse
	commitErr  error

	lastAbandon *genv1.AbandonRequest
	abandonErr  error

	lastRelease *genv1.ReleaseRequest
	releaseErr  error

	lastSplit *genv1.SplitScopeRequest
	splitResp *genv1.SplitScopeResponse
	splitErr  error

	lastConflict *genv1.ClaimScopesConflictRequest
	conflictResp *genv1.ScopesConflictResponse
	conflictErr  error
}

func (f *fakeCPServer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return f.capsResp, f.capsErr
}

func (f *fakeCPServer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	f.lastOpen = req
	return f.openResp, f.openErr
}

func (f *fakeCPServer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	f.lastCommit = req
	return f.commitResp, f.commitErr
}

func (f *fakeCPServer) Abandon(_ context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	f.lastAbandon = req
	return &genv1.AbandonResponse{}, f.abandonErr
}

func (f *fakeCPServer) Release(_ context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	f.lastRelease = req
	return &genv1.ReleaseResponse{}, f.releaseErr
}

func (f *fakeCPServer) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	f.lastSplit = req
	return f.splitResp, f.splitErr
}

func (f *fakeCPServer) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	f.lastConflict = req
	return f.conflictResp, f.conflictErr
}

func syncCaps() *genv1.CapabilitiesResponse {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		SupportsSplitScope:    true,
		DeclaredErrorClasses:  []string{"fs/root_unavailable"},
	}
}

func backedOver(t *testing.T, srv *fakeCPServer) *ServerBackedClaimProducer {
	t.Helper()
	p, err := NewServerBackedClaimProducer(context.Background(), "items", srv)
	if err != nil {
		t.Fatalf("NewServerBackedClaimProducer: %v", err)
	}
	return p
}

func TestServerBackedCapabilitiesConvertedOnce(t *testing.T) {
	p := backedOver(t, &fakeCPServer{capsResp: syncCaps()})
	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.WriteSemanticsAllowed) != 1 || caps.WriteSemanticsAllowed[0] != claimproducer.WriteSemanticsSync {
		t.Fatalf("envelope: got %v", caps.WriteSemanticsAllowed)
	}
	if !caps.SupportsSplitScope {
		t.Fatal("SupportsSplitScope must survive conversion")
	}
	if len(caps.DeclaredErrorClasses) != 1 || caps.DeclaredErrorClasses[0] != "fs/root_unavailable" {
		t.Fatalf("DeclaredErrorClasses: got %v", caps.DeclaredErrorClasses)
	}
	if p.Name() != "items" {
		t.Fatalf("Name: got %q", p.Name())
	}
}

func TestServerBackedConstructionRejectsBadCapabilities(t *testing.T) {
	cases := []struct {
		name string
		srv  *fakeCPServer
		want string
	}{
		{"rpc error", &fakeCPServer{capsErr: errors.New("boom")}, "Capabilities: boom"},
		{"unknown ws", &fakeCPServer{capsResp: &genv1.CapabilitiesResponse{WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_UNKNOWN}}}, "UNKNOWN write_semantics"},
		{"empty ws", &fakeCPServer{capsResp: &genv1.CapabilitiesResponse{}}, "empty write_semantics_allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewServerBackedClaimProducer(context.Background(), "items", tc.srv)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestServerBackedConstructionRequiresNameAndServer(t *testing.T) {
	if _, err := NewServerBackedClaimProducer(context.Background(), "", &fakeCPServer{capsResp: syncCaps()}); err == nil {
		t.Fatal("empty name must fail")
	}
	if _, err := NewServerBackedClaimProducer(context.Background(), "items", nil); err == nil {
		t.Fatal("nil server must fail")
	}
}

func TestServerBackedOpenConvertsAcquired(t *testing.T) {
	srv := &fakeCPServer{
		capsResp: syncCaps(),
		openResp: &genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
			Address:                []byte(`{"path":"a"}`),
			Payload:                []byte(`{"p":1}`),
			ClaimScope:             []byte(`{"s":1}`),
			RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
		}}},
	}
	p := backedOver(t, srv)
	out, err := p.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{
		ProducerName: "items",
		Selector:     "@sel",
		Intent:       claimproducer.IntentReadWrite,
		Alias:        "a1",
		TemplateID:   "t1",
		InstanceID:   "i1",
		RunScopeID:   "rs1",
		Lifetime:     "durable",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !out.Available || out.Result.RealizedWriteSemantics != claimproducer.WriteSemanticsSync {
		t.Fatalf("outcome: %+v", out)
	}
	if string(out.Result.Address) != `{"path":"a"}` {
		t.Fatalf("address: %s", out.Result.Address)
	}
	req := srv.lastOpen
	if req.GetClaimId() != "claim-1" || req.GetProducerName() != "items" || req.GetSelector() != "@sel" ||
		req.GetIntent() != "rw" || req.GetAlias() != "a1" || req.GetTemplateId() != "t1" ||
		req.GetInstanceId() != "i1" || req.GetRunScopeId() != "rs1" || req.GetLifetime() != "durable" {
		t.Fatalf("OpenRequest fields dropped: %+v", req)
	}
}

func TestServerBackedOpenConvertsUnavailable(t *testing.T) {
	srv := &fakeCPServer{
		capsResp: syncCaps(),
		openResp: &genv1.OpenResponse{Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{ErrorClass: "fs/root_unavailable"}}},
	}
	p := backedOver(t, srv)
	out, err := p.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if out.Available || out.UnavailableClass != "fs/root_unavailable" {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestServerBackedOpenRejectsMissingOutcome(t *testing.T) {
	p := backedOver(t, &fakeCPServer{capsResp: syncCaps(), openResp: &genv1.OpenResponse{}})
	_, err := p.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{})
	if !errors.Is(err, ErrOpenResponseMissingOutcome) {
		t.Fatalf("got %v, want ErrOpenResponseMissingOutcome", err)
	}
}

func TestServerBackedErrorsPassThroughUnwrapped(t *testing.T) {
	boom := errors.New("boom")
	srv := &fakeCPServer{capsResp: syncCaps(), openErr: boom, commitErr: boom, abandonErr: boom, releaseErr: boom, splitErr: boom, conflictErr: boom}
	p := backedOver(t, srv)
	ctx := context.Background()
	if _, err := p.Open(ctx, "c", claimproducer.ClaimSpec{}); !errors.Is(err, boom) {
		t.Fatalf("Open: %v", err)
	}
	if _, err := p.Commit(ctx, "c", nil, nil, ""); !errors.Is(err, boom) {
		t.Fatalf("Commit: %v", err)
	}
	if err := p.Abandon(ctx, "c", nil, nil, ""); !errors.Is(err, boom) {
		t.Fatalf("Abandon: %v", err)
	}
	if err := p.Release(ctx, "c", nil, nil, ""); !errors.Is(err, boom) {
		t.Fatalf("Release: %v", err)
	}
	if _, err := p.SplitScope(ctx, claimproducer.SplitClaimScopeRequest{}); !errors.Is(err, boom) {
		t.Fatalf("SplitScope: %v", err)
	}
	if _, err := p.ScopesConflict(ctx, nil, nil); !errors.Is(err, boom) {
		t.Fatalf("ScopesConflict: %v", err)
	}
}

func TestServerBackedCommitAbandonReleaseCarryArgs(t *testing.T) {
	srv := &fakeCPServer{
		capsResp:   syncCaps(),
		commitResp: &genv1.CommitResponse{VersionId: "v7", ProducerMetadata: []byte("m")},
	}
	p := backedOver(t, srv)
	ctx := context.Background()
	res, err := p.Commit(ctx, "c1", []byte("scope"), []byte("addr"), "")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.VersionID != "v7" || string(res.ProducerMetadata) != "m" {
		t.Fatalf("CommitResult: %+v", res)
	}
	if srv.lastCommit.GetClaimId() != "c1" || string(srv.lastCommit.GetClaimScope()) != "scope" || string(srv.lastCommit.GetAddress()) != "addr" {
		t.Fatalf("CommitRequest: %+v", srv.lastCommit)
	}
	if err := p.Abandon(ctx, "c2", []byte("s2"), []byte("a2"), ""); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if srv.lastAbandon.GetClaimId() != "c2" || string(srv.lastAbandon.GetClaimScope()) != "s2" || string(srv.lastAbandon.GetAddress()) != "a2" {
		t.Fatalf("AbandonRequest: %+v", srv.lastAbandon)
	}
	if err := p.Release(ctx, "c3", []byte("s3"), []byte("a3"), ""); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if srv.lastRelease.GetClaimId() != "c3" || string(srv.lastRelease.GetClaimScope()) != "s3" || string(srv.lastRelease.GetAddress()) != "a3" {
		t.Fatalf("ReleaseRequest: %+v", srv.lastRelease)
	}
}

func TestServerBackedSplitScopeConverts(t *testing.T) {
	srv := &fakeCPServer{
		capsResp: syncCaps(),
		splitResp: &genv1.SplitScopeResponse{SubScopes: []*genv1.SubScopeDescriptor{{
			ClaimScopeData:   []byte("csd"),
			PartitionKey:     "pk",
			ProducerMetadata: []byte("pm"),
			Address:          []byte("ad"),
			Payload:          []byte("pl"),
		}}},
	}
	p := backedOver(t, srv)
	resp, err := p.SplitScope(context.Background(), claimproducer.SplitClaimScopeRequest{ClaimHandleID: "h1", PartitionRequest: []byte("pr")})
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if srv.lastSplit.GetClaimHandleId() != "h1" || string(srv.lastSplit.GetPartitionRequest()) != "pr" {
		t.Fatalf("SplitScopeRequest: %+v", srv.lastSplit)
	}
	if len(resp.SubClaimScopes) != 1 {
		t.Fatalf("SubClaimScopes: %+v", resp.SubClaimScopes)
	}
	sub := resp.SubClaimScopes[0]
	if string(sub.ClaimScopeData) != "csd" || sub.PartitionKey != "pk" || string(sub.ProducerMetadata) != "pm" || string(sub.Address) != "ad" || string(sub.Payload) != "pl" {
		t.Fatalf("descriptor: %+v", sub)
	}
}

func TestServerBackedScopesConflictConverts(t *testing.T) {
	srv := &fakeCPServer{capsResp: syncCaps(), conflictResp: &genv1.ScopesConflictResponse{Conflicts: true}}
	p := backedOver(t, srv)
	got, err := p.ScopesConflict(context.Background(), []byte("a"), []byte("b"))
	if err != nil {
		t.Fatalf("ScopesConflict: %v", err)
	}
	if !got {
		t.Fatal("Conflicts must survive conversion")
	}
	if string(srv.lastConflict.GetClaimScopeA()) != "a" || string(srv.lastConflict.GetClaimScopeB()) != "b" {
		t.Fatalf("ClaimScopesConflictRequest: %+v", srv.lastConflict)
	}
}
