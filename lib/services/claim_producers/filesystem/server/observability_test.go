// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"

	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestObservability_Capabilities(t *testing.T) {
	st, err := fsstore.New(fsstore.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	obs := NewObservabilityServer(st, t.TempDir(), nil)
	caps, err := obs.Capabilities(context.Background(), &genv1.GetClaimProducerCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.SupportsClaimGet || !caps.SupportsClaimStream || !caps.SupportsListClaims {
		t.Fatalf("capabilities = %+v; want all three claim-* flags true", caps)
	}
	if caps.RetentionAfterTerminalSeconds != 0 {
		t.Fatalf("retention_after_terminal_seconds = %d, want 0 (eviction is count-based only; the store advertises no time-based retention guarantee)", caps.RetentionAfterTerminalSeconds)
	}
}

func TestObservability_GetClaim_AfterCommit(t *testing.T) {
	st, _ := fsstore.New(fsstore.Config{Root: t.TempDir()})
	if _, err := st.Open(context.Background(), "c1", "foo"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Commit(context.Background(), "c1", nil, nil, ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	obs := NewObservabilityServer(st, t.TempDir(), nil)
	d, err := obs.GetClaim(context.Background(), &genv1.GetClaimRequest{ClaimId: "c1"})
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if d.State != genv1.ClaimState_COMMITTED {
		t.Fatalf("state = %v, want COMMITTED", d.State)
	}
	if len(d.History) != 2 {
		t.Fatalf("history = %d events, want 2 (open+commit)", len(d.History))
	}
}

func TestObservability_GetClaim_Unknown(t *testing.T) {
	st, _ := fsstore.New(fsstore.Config{Root: t.TempDir()})
	obs := NewObservabilityServer(st, t.TempDir(), nil)
	d, err := obs.GetClaim(context.Background(), &genv1.GetClaimRequest{ClaimId: "missing"})
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if d.State != genv1.ClaimState_UNKNOWN {
		t.Fatalf("state = %v, want UNKNOWN", d.State)
	}
}

func TestObservability_ListClaims(t *testing.T) {
	st, _ := fsstore.New(fsstore.Config{Root: t.TempDir()})
	if _, err := st.Open(context.Background(), "c1", "x"); err != nil {
		t.Fatalf("Open c1: %v", err)
	}
	if _, err := st.Open(context.Background(), "c2", "y"); err != nil {
		t.Fatalf("Open c2: %v", err)
	}
	obs := NewObservabilityServer(st, t.TempDir(), nil)
	res, err := obs.ListClaims(context.Background(), &genv1.ListClaimsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}
	if len(res.Claims) != 2 {
		t.Fatalf("claims = %d, want 2", len(res.Claims))
	}
}

type fakeStreamServer struct {
	genv1.ClaimProducerObservability_StreamClaimServer
	ctx    context.Context
	events []*genv1.ClaimEvent
}

func (f *fakeStreamServer) Send(ev *genv1.ClaimEvent) error {
	f.events = append(f.events, ev)
	return nil
}
func (f *fakeStreamServer) Context() context.Context     { return f.ctx }
func (f *fakeStreamServer) SendHeader(metadata.MD) error { return nil }
func (f *fakeStreamServer) SetHeader(metadata.MD) error  { return nil }
func (f *fakeStreamServer) SetTrailer(metadata.MD)       {}

func TestObservability_StreamClaim_AfterTerminal(t *testing.T) {
	st, _ := fsstore.New(fsstore.Config{Root: t.TempDir()})
	if _, err := st.Open(context.Background(), "c1", "foo"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Commit(context.Background(), "c1", nil, nil, ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	obs := NewObservabilityServer(st, t.TempDir(), nil)
	f := &fakeStreamServer{ctx: context.Background()}
	if err := obs.StreamClaim(&genv1.StreamClaimRequest{ClaimId: "c1"}, f); err != nil {
		t.Fatalf("StreamClaim: %v", err)
	}
	if len(f.events) != 3 {
		t.Fatalf("events = %d, want 3", len(f.events))
	}
	if f.events[len(f.events)-1].Category != "claim_terminal" {
		t.Fatalf("last event = %s, want claim_terminal", f.events[len(f.events)-1].Category)
	}
}
