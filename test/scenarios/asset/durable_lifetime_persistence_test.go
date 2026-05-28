// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N6 scenario — durable_lifetime_persistence.
//
// `lifetime: durable` claims survive auto-terminal Commit as
// state='committed' rows on rimsky_claim_handles. The N6 contract
// pinned here is the lifetime taxonomy: subgraph (default) vs.
// durable. Post-Stage-3 of the 2026-05-17 claim-handle state-column
// refactor, the auto-terminal Promote flips state from active to
// committed and the row remains alive until
// ReleaseHeldDurableClaims (instance termination) or the operator
// DELETE /assets/{alias} handler. The full end-to-end (auto-terminal
// → Promote → ListByInstanceAndState → ReleaseHeldDurableClaims)
// needs the postgres harness; this scenario pins the foundation/spec
// lifetime constants + the runtime ClaimHandleInsertInput shape.
package asset

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestDurableLifetimePersistence_TaxonomyConstants(t *testing.T) {
	t.Parallel()
	if spec.ClaimLifetimeSubgraph != "subgraph" {
		t.Errorf("ClaimLifetimeSubgraph = %q, want subgraph", spec.ClaimLifetimeSubgraph)
	}
	if spec.ClaimLifetimeDurable != "durable" {
		t.Errorf("ClaimLifetimeDurable = %q, want durable", spec.ClaimLifetimeDurable)
	}
}

func TestDurableLifetimePersistence_InsertInputCarriesLifetime(t *testing.T) {
	t.Parallel()
	in := persistence.ClaimHandleInsertInput{
		Lifetime: spec.ClaimLifetimeDurable,
		IsHeld:   true,
	}
	if in.Lifetime != "durable" {
		t.Errorf("Lifetime: got %q want durable", in.Lifetime)
	}
	if !in.IsHeld {
		t.Errorf("IsHeld should be true for durable held subgraphs")
	}
}
