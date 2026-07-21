// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import "testing"

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
