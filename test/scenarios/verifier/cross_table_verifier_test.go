// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
