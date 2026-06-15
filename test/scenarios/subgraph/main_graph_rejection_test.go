// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N3 scenario — main_graph_rejection.
//
// The `main` graph (the reserved top-level graph) is the instance-level
// graph; entry/exit have no meaning at instance scope. Templates that
// declare entry/exit on the main graph are rejected at registration
// with the `subgraph_main_has_entry_or_exit` class per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs / Edge-case rejections at registration.
//
// This scenario exercises the validator directly (no full-stack
// harness boot) — the rejection happens at template canonicalization,
// well before any runtime wiring.
package subgraph

import (
	"strings"
	"testing"

	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestMainGraphWithEntryRejected(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:                "main-has-entry",
		Version:             "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Graphs: []tmplspec.GraphSpec{
			{
				// @constraint: main MUST NOT declare entry or exit; the validator
				// enforces this with the subgraph_main_has_entry_or_exit
				// rejection class.
				Name:  tmplspec.MainGraphName,
				Entry: "root",
				Nodes: []node.TemplateNodeDef{{Type: "root"}},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	var found bool
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "subgraph_main_has_entry_or_exit") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected subgraph_main_has_entry_or_exit rejection; got: %v", res.Errors)
	}
}

func TestMainGraphWithExitRejected(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:                "main-has-exit",
		Version:             "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Exit:  "root",
				Nodes: []node.TemplateNodeDef{{Type: "root"}},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	var found bool
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "subgraph_main_has_entry_or_exit") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected subgraph_main_has_entry_or_exit rejection; got: %v", res.Errors)
	}
}
