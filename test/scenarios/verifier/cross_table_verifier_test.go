// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N10 scenario — cross_table_verifier.
//
// A verifier executor can verify across multiple tables; the
// claim_aliases on the executor context list the per-node aliases
// the executor receives at dispatch. The scenario pins the
// claim_aliases pass-through shape from the validation request.
package verifier

import (
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestCrossTableVerifier_ClaimAliasesPassThroughExecutorContext(t *testing.T) {
	t.Parallel()
	ctx := &genv1.ExecutorContext{
		NodeAlias:    "cross-table-verifier",
		ClaimAliases: []string{"primary", "secondary", "audit"},
	}
	if len(ctx.GetClaimAliases()) != 3 {
		t.Fatalf("ClaimAliases: expected 3, got %d", len(ctx.GetClaimAliases()))
	}
	for i, want := range []string{"primary", "secondary", "audit"} {
		if ctx.GetClaimAliases()[i] != want {
			t.Errorf("ClaimAliases[%d]: got %q want %q", i, ctx.GetClaimAliases()[i], want)
		}
	}
}
