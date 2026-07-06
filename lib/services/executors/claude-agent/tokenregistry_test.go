// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import "testing"

func makeEntry(runID string) *TokenEntry {
	return &TokenEntry{
		SessionID:         runID,
		AttributesAtSpawn: map[string]any{},
		DispatchContext:   NewDispatchContextSnapshot("d-1", "rs-1", "", "", nil),
		CancelToken:       "ct",
		NodeID:            "n-1",
		CallbackURL:       "http://supervisor.invalid/cb",
		OnComplete: func(map[string]any, bool, *string, []string, ScheduleTeardown) (CompleteResult, error) {
			return CompleteResult{Accepted: true}, nil
		},
		OnBlocked: func(string, any, ScheduleTeardown) error { return nil },
		OnError:   func(string, any, ScheduleTeardown) error { return nil },
	}
}

func TestTokenRegistryRegisterLookupRelease(t *testing.T) {
	reg := NewTokenRegistry()
	entry := makeEntry("run-1")
	reg.Register("tok-1", entry)
	if reg.Size() != 1 {
		t.Fatalf("size = %d, want 1", reg.Size())
	}
	got, ok := reg.Lookup("tok-1")
	if !ok || got != entry {
		t.Fatalf("lookup returned %v, %v", got, ok)
	}
	reg.Release("tok-1")
	if reg.Size() != 0 {
		t.Fatalf("size after release = %d, want 0", reg.Size())
	}
	if _, ok := reg.Lookup("tok-1"); ok {
		t.Fatal("expected lookup miss after release")
	}
}

func TestTokenRegistryLookupUnknownToken(t *testing.T) {
	reg := NewTokenRegistry()
	if _, ok := reg.Lookup("nope"); ok {
		t.Fatal("expected miss for unknown token")
	}
}

func TestDispatchContextSnapshotWarnsOnMissingDisposition(t *testing.T) {
	var warned *WireContractViolation
	snap := NewDispatchContextSnapshot("d-1", "rs-1", "prior-1", "PRIOR_NONE", func(e WireContractViolation) {
		warned = &e
	})
	if warned == nil {
		t.Fatal("expected wire-contract warning")
	}
	if warned.PriorNodeRunID != "prior-1" || warned.Kind != "wire_contract_violation" {
		t.Fatalf("unexpected warning %+v", warned)
	}
	if snap.PriorNodeRunID == nil || *snap.PriorNodeRunID != "prior-1" {
		t.Fatalf("expected prior id preserved, got %+v", snap)
	}
	if snap.PriorDispatchDisposition != nil {
		t.Fatalf("expected nil disposition, got %+v", snap.PriorDispatchDisposition)
	}
}

func TestDispatchContextSnapshotMapsTypedDispositions(t *testing.T) {
	cases := map[string]PriorDispatchDisposition{
		"PRIOR_STALE_RECOVERY":    PriorStaleRecovery,
		"PRIOR_RETRY_AFTER_ERROR": PriorRetryAfterError,
		"PRIOR_RECALCULATE":       PriorRecalculate,
	}
	for wire, want := range cases {
		snap := NewDispatchContextSnapshot("d-1", "rs-1", "prior-1", wire, func(WireContractViolation) {
			t.Fatalf("unexpected warning for wire %q", wire)
		})
		if snap.PriorDispatchDisposition == nil || *snap.PriorDispatchDisposition != want {
			t.Fatalf("wire %q mapped to %+v, want %v", wire, snap.PriorDispatchDisposition, want)
		}
	}
}

func TestDispatchContextSnapshotNoPriorMeansNoWarning(t *testing.T) {
	snap := NewDispatchContextSnapshot("d-1", "rs-1", "", "", func(WireContractViolation) {
		t.Fatal("unexpected warning without a prior dispatch id")
	})
	if snap.PriorNodeRunID != nil || snap.PriorDispatchDisposition != nil {
		t.Fatalf("expected nil prior fields, got %+v", snap)
	}
}
