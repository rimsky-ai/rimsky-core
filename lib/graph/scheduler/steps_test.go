// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scheduler

import (
	"testing"

	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestAcquiresClaims(t *testing.T) {
	t.Parallel()
	if AcquiresClaims(nil) {
		t.Fatal("AcquiresClaims(nil) must be false")
	}
	if AcquiresClaims(&nodepkg.TemplateNodeDef{}) {
		t.Fatal("AcquiresClaims must be false for a node with no claim producers")
	}
	def := &nodepkg.TemplateNodeDef{
		ClaimProducers: []nodepkg.NodeClaimProducerRef{{Name: "store-a"}},
	}
	if !AcquiresClaims(def) {
		t.Fatal("AcquiresClaims must be true for a node declaring a claim producer")
	}
}
