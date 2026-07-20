// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func TestFakeOpenDefaultRealizesWriteSemanticsFromCapabilities(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{
			claimproducer.WriteSemanticsBlockingAsync,
			claimproducer.WriteSemanticsSync,
		},
	})

	out, err := f.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{Selector: "sel"})
	if err != nil {
		t.Fatalf("Open: unexpected error: %v", err)
	}
	if !out.Available {
		t.Fatalf("Open: expected Available=true")
	}
	if out.Result.RealizedWriteSemantics != claimproducer.WriteSemanticsBlockingAsync {
		t.Fatalf("RealizedWriteSemantics = %q, want %q (first entry of WriteSemanticsAllowed)",
			out.Result.RealizedWriteSemantics, claimproducer.WriteSemanticsBlockingAsync)
	}
}

func TestFakeOpenDefaultRealizedSemanticsWithinAdvertisedEnvelope(t *testing.T) {
	caps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsReadOnly},
	}
	f := NewFake("alpha", caps)

	out, err := f.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{Selector: "sel"})
	if err != nil {
		t.Fatalf("Open: unexpected error: %v", err)
	}
	if !caps.Contains(out.Result.RealizedWriteSemantics) {
		t.Fatalf("RealizedWriteSemantics %q not in advertised envelope %v",
			out.Result.RealizedWriteSemantics, caps.WriteSemanticsAllowed)
	}
	if out.Result.RealizedWriteSemantics == claimproducer.WriteSemanticsUnknown {
		t.Fatalf("RealizedWriteSemantics must not be the wire zero value")
	}
}

func TestFakeOpenNoAdvertisedSemanticsLeavesUnknown(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{})

	out, err := f.Open(context.Background(), "claim-1", claimproducer.ClaimSpec{Selector: "sel"})
	if err != nil {
		t.Fatalf("Open: unexpected error: %v", err)
	}
	if out.Result.RealizedWriteSemantics != claimproducer.WriteSemanticsUnknown {
		t.Fatalf("RealizedWriteSemantics = %q, want the zero value when no semantics are advertised",
			out.Result.RealizedWriteSemantics)
	}
}

func TestFakeOpenRecordsFullClaimSpec(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	spec := claimproducer.ClaimSpec{
		ProducerName: "alpha",
		Selector:     "rimsky/things/1",
		Intent:       claimproducer.IntentReadWrite,
		Alias:        "the-alias",
		TemplateID:   "tmpl-1",
		InstanceID:   "inst-1",
		RunScopeID:   "run-1",
		Lifetime:     "durable",
	}

	if _, err := f.Open(context.Background(), "claim-1", spec); err != nil {
		t.Fatalf("Open: unexpected error: %v", err)
	}

	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("Calls() = %d entries, want 1", len(calls))
	}
	got := calls[0]
	if got.Selector != spec.Selector {
		t.Errorf("Selector = %q, want %q", got.Selector, spec.Selector)
	}
	if got.Intent != spec.Intent {
		t.Errorf("Intent = %q, want %q", got.Intent, spec.Intent)
	}
	if got.TemplateID != spec.TemplateID {
		t.Errorf("TemplateID = %q, want %q", got.TemplateID, spec.TemplateID)
	}
	if got.InstanceID != spec.InstanceID {
		t.Errorf("InstanceID = %q, want %q", got.InstanceID, spec.InstanceID)
	}
	if got.RunScopeID != spec.RunScopeID {
		t.Errorf("RunScopeID = %q, want %q", got.RunScopeID, spec.RunScopeID)
	}
	if got.Alias != spec.Alias {
		t.Errorf("Alias = %q, want %q", got.Alias, spec.Alias)
	}
	if got.Lifetime != spec.Lifetime {
		t.Errorf("Lifetime = %q, want %q", got.Lifetime, spec.Lifetime)
	}
}

func TestFakeScopesConflictDefaultUnsupportedWhenCapabilityNotAdvertised(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{SupportsScopesConflict: false})

	_, err := f.ScopesConflict(context.Background(), []byte("a"), []byte("a"))
	if !errors.Is(err, claimproducer.ErrScopesConflictUnsupported) {
		t.Fatalf("ScopesConflict error = %v, want %v", err, claimproducer.ErrScopesConflictUnsupported)
	}
}

func TestFakeScopesConflictDefaultByteEqualWhenSupported(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{SupportsScopesConflict: true})

	cases := []struct {
		name string
		a, b []byte
		want bool
	}{
		{"both empty", nil, nil, false},
		{"a empty", nil, []byte("x"), false},
		{"identical", []byte("scope"), []byte("scope"), true},
		{"different", []byte("scope-a"), []byte("scope-b"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.ScopesConflict(context.Background(), tc.a, tc.b)
			if err != nil {
				t.Fatalf("ScopesConflict: unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ScopesConflict(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestFakeScopesConflictFuncOverrideBypassesCapabilityGate(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{SupportsScopesConflict: false})
	f.ScopesConflictFunc = func(a, b []byte) (bool, error) {
		return true, nil
	}

	got, err := f.ScopesConflict(context.Background(), []byte("a"), []byte("b"))
	if err != nil {
		t.Fatalf("ScopesConflict: unexpected error: %v", err)
	}
	if !got {
		t.Fatalf("ScopesConflict: expected override result true")
	}
}

func TestFakeOnRunScopeTerminalRecordsFullRequest(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{})
	req := locks.OnRunScopeTerminalRequest{
		RunScopeID:     "run-9",
		TerminalReason: "cascade-cancelled",
		InstanceID:     "inst-9",
	}

	if err := f.OnRunScopeTerminal(context.Background(), req); err != nil {
		t.Fatalf("OnRunScopeTerminal: unexpected error: %v", err)
	}

	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("Calls() = %d entries, want 1", len(calls))
	}
	got := calls[0]
	if got.RunScopeID != "run-9" {
		t.Errorf("RunScopeID = %q, want %q", got.RunScopeID, "run-9")
	}
	if got.TerminalReason != "cascade-cancelled" {
		t.Errorf("TerminalReason = %q, want %q", got.TerminalReason, "cascade-cancelled")
	}
	if got.InstanceID != "inst-9" {
		t.Errorf("InstanceID = %q, want %q", got.InstanceID, "inst-9")
	}
}

func TestFakeOnInstanceCreatedRecordsFullRequest(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{})
	req := locks.OnInstanceCreatedRequest{
		InstanceID:      "inst-1",
		TemplateHash:    "tmpl-1",
		InstanceKey:     "inst-key-1",
		Params:          []byte(`{"p":1}`),
		ServiceBindings: []byte(`{"b":2}`),
		OwnerAPIKeyID:   "owner-1",
	}

	if err := f.OnInstanceCreated(context.Background(), req); err != nil {
		t.Fatalf("OnInstanceCreated: unexpected error: %v", err)
	}

	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("Calls() = %d entries, want 1", len(calls))
	}
	got := calls[0]
	if got.InstanceID != "inst-1" {
		t.Errorf("InstanceID = %q, want %q", got.InstanceID, "inst-1")
	}
	if got.TemplateHash != "tmpl-1" {
		t.Errorf("TemplateHash = %q, want %q", got.TemplateHash, "tmpl-1")
	}
	if got.InstanceKey != "inst-key-1" {
		t.Errorf("InstanceKey = %q, want %q", got.InstanceKey, "inst-key-1")
	}
	if string(got.Params) != `{"p":1}` {
		t.Errorf("Params = %q, want %q", got.Params, `{"p":1}`)
	}
	if string(got.ServiceBindings) != `{"b":2}` {
		t.Errorf("ServiceBindings = %q, want %q", got.ServiceBindings, `{"b":2}`)
	}
	if got.OwnerAPIKeyID != "owner-1" {
		t.Errorf("OwnerAPIKeyID = %q, want %q", got.OwnerAPIKeyID, "owner-1")
	}
}

func TestFakeOnInstanceTerminatedRecordsFullRequest(t *testing.T) {
	f := NewFake("alpha", claimproducer.Capabilities{})
	req := locks.OnInstanceTerminatedRequest{
		InstanceID:         "inst-2",
		TemplateHash:       "tmpl-2",
		TerminatedAtUnixMs: 1234567890,
	}

	if err := f.OnInstanceTerminated(context.Background(), req); err != nil {
		t.Fatalf("OnInstanceTerminated: unexpected error: %v", err)
	}

	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("Calls() = %d entries, want 1", len(calls))
	}
	got := calls[0]
	if got.InstanceID != "inst-2" {
		t.Errorf("InstanceID = %q, want %q", got.InstanceID, "inst-2")
	}
	if got.TemplateHash != "tmpl-2" {
		t.Errorf("TemplateHash = %q, want %q", got.TemplateHash, "tmpl-2")
	}
	if got.TerminatedAtUnixMs != 1234567890 {
		t.Errorf("TerminatedAtUnixMs = %d, want %d", got.TerminatedAtUnixMs, 1234567890)
	}
}
