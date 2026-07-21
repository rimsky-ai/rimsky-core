// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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

func TestMatchesClaimScope(t *testing.T) {
	encoded, err := json.Marshal("items/x")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !matchesClaimScope(encoded, "items/x") {
		t.Fatalf("expected claim-scope bytes to match the same selector")
	}
	if matchesClaimScope(encoded, "items/y") {
		t.Fatalf("expected claim-scope bytes to not match a different selector")
	}
	if matchesClaimScope(nil, "items/x") {
		t.Fatalf("expected empty claim-scope data to never match")
	}
}

func TestPickAliasForClaimHandle_SinglePickShortCircuits(t *testing.T) {
	picks := []aliasCandidate{{acquirerType: "a", alias: "only-alias"}}
	got := pickAliasForClaimHandle(context.Background(), RunArgs{}, nil, shared.UUID{}, "a", picks, &persistence.ClaimHandleRow{})
	if got != "only-alias" {
		t.Fatalf("got %q, want %q", got, "only-alias")
	}
}
