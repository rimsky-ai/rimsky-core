// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

const breakpointSnapshotClaimSentinel = "sk-breakpoint-snapshot-must-not-leak-claim-content"

func TestHeldClaimsSummaryForBreakpoint_OmitsClaimContent(t *testing.T) {
	sentinel := breakpointSnapshotClaimSentinel
	acq := &acquisition{
		Locks: []AcquiredLock{
			{
				ClaimHandleID: shared.UUID{},
				Alias:         "topics-ring",
				Producer:      &abandonStub{},
				Spec: claimproducer.ClaimSpec{
					Alias:  "topics-ring",
					Intent: claimproducer.IntentReadWrite,
				},
				ClaimResult: claimproducer.ClaimResult{
					Address:    json.RawMessage(`{"path":"` + sentinel + `"}`),
					Payload:    json.RawMessage(`{"body":"` + sentinel + `"}`),
					ClaimScope: json.RawMessage(`{"scope":"` + sentinel + `"}`),
				},
			},
		},
		HeldClaims: map[string]claimproducer.ClaimResult{
			"content": {
				Address:    json.RawMessage(`{"path":"` + sentinel + `-held"}`),
				Payload:    json.RawMessage(`{"body":"` + sentinel + `-held"}`),
				ClaimScope: json.RawMessage(`{"scope":"` + sentinel + `-held"}`),
			},
		},
	}

	got := heldClaimsSummaryForBreakpoint(acq)
	if len(got) != 2 {
		t.Fatalf("heldClaimsSummaryForBreakpoint: got %d entries, want 2 (one acquired, one held)", len(got))
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(heldClaimsSummaryForBreakpoint result): %v", err)
	}
	if strings.Contains(string(raw), sentinel) {
		t.Fatalf("held-claims breakpoint summary must never carry claim address/payload/scope bytes; "+
			"found sentinel in serialized snapshot: %s", raw)
	}

	allowedKeys := map[string]bool{
		"claim_handle_id": true,
		"alias":           true,
		"intent":          true,
		"producer":        true,
		"source":          true,
	}
	for _, entry := range got {
		for k := range entry {
			if !allowedKeys[k] {
				t.Errorf("held-claims breakpoint summary entry has unexpected key %q "+
					"(only claim_handle_id/alias/intent/producer/source are permitted; a new key may be "+
					"leaking claim content into the debugger snapshot): %v", k, entry)
			}
		}
	}
}

func TestHeldClaimsSummaryForBreakpoint_HeldAliasesInDeterministicOrder(t *testing.T) {
	acq := &acquisition{
		HeldClaims: map[string]claimproducer.ClaimResult{
			"zeta":  {},
			"alpha": {},
			"mid":   {},
		},
	}

	for i := 0; i < 20; i++ {
		got := heldClaimsSummaryForBreakpoint(acq)
		if len(got) != 3 {
			t.Fatalf("iteration %d: got %d entries, want 3", i, len(got))
		}
		wantOrder := []string{"alpha", "mid", "zeta"}
		for j, want := range wantOrder {
			if got[j]["alias"] != want {
				t.Fatalf("iteration %d: entry[%d].alias=%v want %q (held aliases must be sorted for deterministic snapshots)", i, j, got[j]["alias"], want)
			}
		}
	}
}

func TestHeldClaimsSummaryForBreakpoint_NilAcquisitionReturnsEmpty(t *testing.T) {
	got := heldClaimsSummaryForBreakpoint(nil)
	if len(got) != 0 {
		t.Fatalf("heldClaimsSummaryForBreakpoint(nil): got %d entries, want 0", len(got))
	}
}
