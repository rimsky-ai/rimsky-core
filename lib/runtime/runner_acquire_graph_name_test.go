// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestGraphContainingNodeType(t *testing.T) {
	graphs := []spec.GraphSpec{
		{
			Name: spec.MainGraphName,
			Nodes: []spec.TemplateNodeDef{
				{Type: "caller"},
			},
		},
		{
			Name: "worker",
			Nodes: []spec.TemplateNodeDef{
				{Type: "inner-entry"},
				{Type: "inner-exit"},
			},
		},
	}

	cases := []struct {
		name     string
		nodeType string
		want     string
	}{
		{"main graph member", "caller", spec.MainGraphName},
		{"subgraph member", "inner-entry", "worker"},
		{"other subgraph member", "inner-exit", "worker"},
		{"unknown node type falls back to main", "does-not-exist-anywhere", spec.MainGraphName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := graphContainingNodeType(graphs, tc.nodeType)
			if got != tc.want {
				t.Fatalf("graphContainingNodeType(%q) = %q, want %q", tc.nodeType, got, tc.want)
			}
		})
	}
}

func TestGraphContainingNodeType_EmptyGraphsFallsBackToMain(t *testing.T) {
	got := graphContainingNodeType(nil, "anything")
	if got != spec.MainGraphName {
		t.Fatalf("graphContainingNodeType(nil graphs) = %q, want %q (the loud MainGraphName fallback)",
			got, spec.MainGraphName)
	}
}
