// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
