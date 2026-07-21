// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestFindHoldingSubgraph(t *testing.T) {
	subgraphs := []node.HoldingSubgraph{
		{AcquirerType: "a", Alias: "primary", Members: []string{"a", "b"}},
		{AcquirerType: "a", Alias: "secondary", Members: []string{"a", "c"}},
	}
	sg, ok := findHoldingSubgraph(subgraphs, "a", "secondary")
	if !ok {
		t.Fatalf("expected to find subgraph for (a, secondary)")
	}
	if sg.Alias != "secondary" {
		t.Fatalf("Alias = %q, want %q", sg.Alias, "secondary")
	}
	if _, ok := findHoldingSubgraph(subgraphs, "a", "nonexistent"); ok {
		t.Fatalf("expected no match for an unregistered alias")
	}
	if _, ok := findHoldingSubgraph(subgraphs, "z", "primary"); ok {
		t.Fatalf("expected no match for an unregistered acquirer type")
	}
}

func TestIsAliasHeld(t *testing.T) {
	held := node.HoldingSubgraph{AcquirerType: "a", Alias: "primary", Members: []string{"a", "b"}}
	notHeld := node.HoldingSubgraph{AcquirerType: "a", Alias: "unheld", Members: []string{"a"}}
	subgraphs := []node.HoldingSubgraph{held, notHeld}
	if !isAliasHeld(subgraphs, "a", "primary") {
		t.Fatalf("expected alias %q to be held", "primary")
	}
	if isAliasHeld(subgraphs, "a", "unheld") {
		t.Fatalf("expected alias %q to not be held", "unheld")
	}
	if isAliasHeld(subgraphs, "a", "missing") {
		t.Fatalf("expected a missing alias to report unheld")
	}
}

func TestMemberOf(t *testing.T) {
	sg := node.HoldingSubgraph{Members: []string{"a", "b", "c"}}
	if !memberOf(sg, "b") {
		t.Fatalf("expected %q to be a member", "b")
	}
	if memberOf(sg, "z") {
		t.Fatalf("expected %q to not be a member", "z")
	}
}
