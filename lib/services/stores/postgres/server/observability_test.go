// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"

	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// newLedgerOnlyServer builds an ObservabilityServer whose underlying
// store has a nil pool — sufficient for exercising every RPC except
// the pool-backed admin views (items_queue). The capabilities and
// per-claim RPCs operate exclusively against the in-memory ledger.
func newLedgerOnlyServer() *ObservabilityServer {
	return &ObservabilityServer{store: pgsstore.NewForTest()}
}

func TestObservability_Capabilities_Postgres(t *testing.T) {
	obs := newLedgerOnlyServer()
	caps, err := obs.Capabilities(context.Background(), &genv1.GetClaimProducerCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.SupportsClaimGet || !caps.SupportsClaimStream || !caps.SupportsListClaims {
		t.Fatalf("capabilities = %+v; want all three claim-* flags true", caps)
	}
	if caps.RetentionAfterTerminalSeconds == 0 {
		t.Fatal("retention_after_terminal_seconds should be non-zero")
	}
	if want := 2; len(caps.AdminViews) != want {
		t.Fatalf("admin_views = %d, want %d", len(caps.AdminViews), want)
	}
}

func TestObservability_GetClaim_Postgres_Unknown(t *testing.T) {
	obs := newLedgerOnlyServer()
	d, err := obs.GetClaim(context.Background(), &genv1.GetClaimRequest{ClaimId: "missing"})
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if d.State != genv1.ClaimState_UNKNOWN {
		t.Fatalf("state = %v, want UNKNOWN", d.State)
	}
}

func TestObservability_GetClaim_Postgres_AfterTerminal(t *testing.T) {
	obs := newLedgerOnlyServer()
	obs.store.Ledger().RecordOpen("c1", "@review-queue", nil, nil)
	obs.store.Ledger().RecordTerminal("c1", "claim_committed", nil)

	d, err := obs.GetClaim(context.Background(), &genv1.GetClaimRequest{ClaimId: "c1"})
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if d.State != genv1.ClaimState_COMMITTED {
		t.Fatalf("state = %v, want COMMITTED", d.State)
	}
	if len(d.History) != 2 {
		t.Fatalf("history = %d, want 2", len(d.History))
	}
}

func TestObservability_ListClaims_Postgres(t *testing.T) {
	obs := newLedgerOnlyServer()
	obs.store.Ledger().RecordOpen("c1", "@a", nil, nil)
	obs.store.Ledger().RecordOpen("c2", "@b", nil, nil)
	res, err := obs.ListClaims(context.Background(), &genv1.ListClaimsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}
	if len(res.Claims) != 2 {
		t.Fatalf("claims = %d, want 2", len(res.Claims))
	}
}

// fakeStreamServer adapts to the StreamClaim server interface for tests.
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

func TestObservability_StreamClaim_Postgres_AfterTerminal(t *testing.T) {
	obs := newLedgerOnlyServer()
	obs.store.Ledger().RecordOpen("c1", "@a", nil, nil)
	obs.store.Ledger().RecordTerminal("c1", "claim_committed", nil)
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

func TestObservability_StreamClaim_Postgres_Unknown(t *testing.T) {
	obs := newLedgerOnlyServer()
	f := &fakeStreamServer{ctx: context.Background()}
	if err := obs.StreamClaim(&genv1.StreamClaimRequest{ClaimId: "missing"}, f); err != nil {
		t.Fatalf("StreamClaim: %v", err)
	}
	if len(f.events) != 1 || f.events[0].Category != "claim_terminal" {
		t.Fatalf("events = %+v, want 1 claim_terminal marker", f.events)
	}
}

func TestObservability_GetAdminView_Postgres_PickPolicies(t *testing.T) {
	obs := newLedgerOnlyServer()
	view, err := obs.GetAdminView(context.Background(), &genv1.GetAdminViewRequest{ViewName: "pick_policies"})
	if err != nil {
		t.Fatalf("GetAdminView: %v", err)
	}
	if view.RenderHint != "table" {
		t.Fatalf("render_hint = %q, want table", view.RenderHint)
	}
	if view.Schema == nil || len(view.Schema.Columns) == 0 {
		t.Fatalf("schema = %+v, want non-empty columns", view.Schema)
	}
}

func TestObservability_GetAdminView_Postgres_UnknownView(t *testing.T) {
	obs := newLedgerOnlyServer()
	_, err := obs.GetAdminView(context.Background(), &genv1.GetAdminViewRequest{ViewName: "no_such_view"})
	if err == nil {
		t.Fatal("expected error for unknown admin view")
	}
}
