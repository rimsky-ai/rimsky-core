// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N3 scenario — entry_absorption.
//
// The canonicalizer absorbs the sub-graph's entry node into the
// calling node: the calling node carries IsSubgraphEntryAbsorbed=true,
// and the runtime supervisor's terminal handler consults this marker
// to route through the sub-graph internal-cascade fire instead of the
// standard run-tree aggregation. Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs / Identity and absorption.
//
// The scenario validates the canonicalizer + runtime predicate
// alignment without booting the full stack: it constructs a
// template-with-graphs, runs ValidateTemplate to canonicalize, then
// asserts both the marker on the calling node AND the matching
// `IsSubgraphCaller` runtime predicate.
package subgraph

import (
	"testing"

	tmplspec "github.com/rimsky-ai/rimsky-core/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/runtime"
)

func TestEntryAbsorption_MarkerEmittedOnCallingNode(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:                "delegate-template",
		Version:             "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					{Type: "outer-caller", Delegate: "staging"},
					{Type: "plain-node", Executor: "stub"},
				},
			},
			{
				Name:  "staging",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []node.TemplateNodeDef{
					{Type: "validate", Executor: "validator"},
					{Type: "transform", Executor: "transformer",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "validate", Type: "terminal/*"}}},
					{Type: "promote", Executor: "promoter",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "transform", Type: "terminal/*"}}},
				},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	if len(res.Errors) != 0 {
		t.Fatalf("validation errors: %v", res.Errors)
	}

	// Index canonicalized flat nodes by type.
	byType := make(map[string]*node.TemplateNodeDef, len(tmpl.Nodes))
	for i := range tmpl.Nodes {
		byType[tmpl.Nodes[i].Type] = &tmpl.Nodes[i]
	}

	caller := byType["outer-caller"]
	if caller == nil {
		t.Fatalf("outer-caller missing from canonicalized template")
	}
	if !caller.IsSubgraphEntryAbsorbed {
		t.Errorf("outer-caller must carry IsSubgraphEntryAbsorbed=true after canonicalization: %+v", caller)
	}
	if !runtime.IsSubgraphCaller(caller) {
		t.Errorf("outer-caller must be recognized by IsSubgraphCaller (Delegate=%q)", caller.Delegate)
	}

	// A non-delegating node must NOT carry the marker.
	plain := byType["plain-node"]
	if plain == nil {
		t.Fatalf("plain-node missing")
	}
	if plain.IsSubgraphEntryAbsorbed {
		t.Errorf("plain-node should not carry IsSubgraphEntryAbsorbed: %+v", plain)
	}
	if runtime.IsSubgraphCaller(plain) {
		t.Errorf("plain-node should not be IsSubgraphCaller (Delegate=%q)", plain.Delegate)
	}
}

// IsSubgraphExit is consulted by the supervisor's terminal handler to
// route exit-node terminals through the CarryExitWriteback carry-rule.
// Verify the predicate aligns with the canonicalized template's
// declared exit.
func TestEntryAbsorption_ExitNodeIdentified(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:                "delegate-template",
		Version:             "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{{Type: "outer-caller", Delegate: "staging"}},
			},
			{
				Name:  "staging",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []node.TemplateNodeDef{
					{Type: "validate", Executor: "stub"},
					{Type: "transform", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "validate", Type: "terminal/*"}}},
					{Type: "promote", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "transform", Type: "terminal/*"}}},
				},
			},
		},
	}
	if res := node.ValidateTemplate(tmpl, node.RegistryHooks{}); len(res.Errors) != 0 {
		t.Fatalf("unexpected validation errors: %v", res.Errors)
	}
	if !runtime.IsSubgraphExit(tmpl, "promote") {
		t.Errorf("promote should be the sub-graph exit")
	}
	if runtime.IsSubgraphExit(tmpl, "validate") {
		t.Errorf("validate is entry, not exit")
	}
	if runtime.IsSubgraphExit(tmpl, "outer-caller") {
		t.Errorf("outer-caller is the calling node in main; not an exit")
	}
	// The canonicalizer must also stamp IsSubgraphExit on the exit
	// node so the runtime terminal handler routes through the
	// carry-rule via acq.NodeDef alone (no template DB lookup).
	byType := make(map[string]*node.TemplateNodeDef, len(tmpl.Nodes))
	for i := range tmpl.Nodes {
		byType[tmpl.Nodes[i].Type] = &tmpl.Nodes[i]
	}
	if exit := byType["promote"]; exit == nil || !exit.IsSubgraphExit {
		t.Errorf("promote must carry IsSubgraphExit=true after canonicalization: %+v", exit)
	}
	if entry := byType["validate"]; entry == nil || entry.IsSubgraphExit {
		t.Errorf("validate is entry; must not carry IsSubgraphExit: %+v", entry)
	}
	if caller := byType["outer-caller"]; caller == nil || caller.IsSubgraphExit {
		t.Errorf("outer-caller is in main; must not carry IsSubgraphExit: %+v", caller)
	}
}
