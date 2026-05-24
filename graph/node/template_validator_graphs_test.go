// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package node

import (
	"strings"
	"testing"
)

// validateMultiGraph helper: invoke ValidateTemplate on a multi-graph
// fixture, return the error messages joined for substring assertions.
func validateMultiGraph(t *testing.T, spec *TemplateSpec) []string {
	t.Helper()
	res := ValidateTemplate(spec, RegistryHooks{})
	msgs := make([]string, 0, len(res.Errors))
	for _, e := range res.Errors {
		msgs = append(msgs, e.Msg)
	}
	return msgs
}

func hasErrorContaining(msgs []string, needle string) bool {
	for _, m := range msgs {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
}

// happy-path: a single `main` graph with one node validates clean.
func TestCanonicalizeGraphs_HappyPathSingleMain(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl-1",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "alpha"},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if len(msgs) != 0 {
		t.Fatalf("expected no errors; got: %v", msgs)
	}
	if len(spec.Nodes) != 1 || spec.Nodes[0].Type != "alpha" {
		t.Fatalf("expected canonicalizer to flatten Nodes to [alpha], got: %+v", spec.Nodes)
	}
}

// reject mixing flat Nodes and Graphs.
func TestCanonicalizeGraphs_RejectGraphsAndNodesBothSet(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Nodes:               []TemplateNodeDef{{Type: "x"}},
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "y"}}},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "graphs_and_nodes_both_set") {
		t.Fatalf("expected graphs_and_nodes_both_set rejection; got: %v", msgs)
	}
}

// reject when no `main` graph is present.
func TestCanonicalizeGraphs_RejectMissingMain(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "b",
				Nodes: []TemplateNodeDef{{Type: "a"}, {Type: "b"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_missing_main") {
		t.Fatalf("expected subgraph_missing_main rejection; got: %v", msgs)
	}
}

// reject `main` having entry/exit.
func TestCanonicalizeGraphs_RejectMainHasEntryExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name:  MainGraphName,
				Entry: "a",
				Nodes: []TemplateNodeDef{{Type: "a"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_main_has_entry_or_exit") {
		t.Fatalf("expected subgraph_main_has_entry_or_exit rejection; got: %v", msgs)
	}
}

// reject sub-graph missing entry/exit.
func TestCanonicalizeGraphs_RejectSubGraphMissingEntry(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Exit:  "b",
				Nodes: []TemplateNodeDef{{Type: "a"}, {Type: "b"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_missing_entry") {
		t.Fatalf("expected subgraph_missing_entry rejection; got: %v", msgs)
	}
}

func TestCanonicalizeGraphs_RejectSubGraphMissingExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "a",
				Nodes: []TemplateNodeDef{{Type: "a"}, {Type: "b"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_missing_exit") {
		t.Fatalf("expected subgraph_missing_exit rejection; got: %v", msgs)
	}
}

// reject entry == exit.
func TestCanonicalizeGraphs_RejectEntryEqualsExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "a",
				Nodes: []TemplateNodeDef{{Type: "a"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_entry_equals_exit") {
		t.Fatalf("expected subgraph_entry_equals_exit rejection; got: %v", msgs)
	}
}

// reject entry naming an unknown node.
func TestCanonicalizeGraphs_RejectUnknownEntry(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "ghost",
				Exit:  "b",
				Nodes: []TemplateNodeDef{{Type: "a"}, {Type: "b"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_unknown_entry") {
		t.Fatalf("expected subgraph_unknown_entry rejection; got: %v", msgs)
	}
}

// disconnected internal node — reachability rejection.
func TestCanonicalizeGraphs_RejectDisconnectedInternalNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "z",
				Nodes: []TemplateNodeDef{
					{Type: "a"},
					{Type: "orphan"}, // no subscriptions; unreachable
					{Type: "z", Subscribes: []SubscriptionEntry{{Node: "a", On: "state"}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_disconnected_internal_node") {
		t.Fatalf("expected subgraph_disconnected_internal_node; got: %v", msgs)
	}
}

// connected sub-graph passes the reachability check.
func TestCanonicalizeGraphs_ReachabilityHappyPath(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "c",
				Nodes: []TemplateNodeDef{
					{Type: "a"},
					{Type: "b", Subscribes: []SubscriptionEntry{{Node: "a", On: "state"}}},
					{Type: "c", Subscribes: []SubscriptionEntry{{Node: "b", On: "state"}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	for _, m := range msgs {
		if strings.Contains(m, "subgraph_disconnected_internal_node") {
			t.Fatalf("expected no disconnect; got: %v", msgs)
		}
	}
}

// delegate cycle detection.
func TestCanonicalizeGraphs_RejectDelegateCycle(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "m", Delegate: "g1"},
				},
			},
			{
				Name:  "g1",
				Entry: "g1n",
				Exit:  "g1x",
				Nodes: []TemplateNodeDef{
					{Type: "g1n", Delegate: "g2"},
					{Type: "g1x", Subscribes: []SubscriptionEntry{{Node: "g1n", On: "state"}}},
				},
			},
			{
				Name:  "g2",
				Entry: "g2n",
				Exit:  "g2x",
				Nodes: []TemplateNodeDef{
					{Type: "g2n", Delegate: "g1"}, // cycle: g1 -> g2 -> g1
					{Type: "g2x", Subscribes: []SubscriptionEntry{{Node: "g2n", On: "state"}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_recursion_unsupported") {
		t.Fatalf("expected subgraph_recursion_unsupported; got: %v", msgs)
	}
}

// internal-node references-outer rejection.
func TestCanonicalizeGraphs_RejectInternalReferencesOuter(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "outer"},
				},
			},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "b",
				Nodes: []TemplateNodeDef{
					{Type: "a"},
					{Type: "b", Subscribes: []SubscriptionEntry{{Node: "outer", On: "state"}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_internal_references_outer") {
		t.Fatalf("expected subgraph_internal_references_outer; got: %v", msgs)
	}
}

// cross-graph node type duplication.
func TestCanonicalizeGraphs_RejectDuplicateNodeTypeAcrossGraphs(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "shared"}}},
			{
				Name:  "sub",
				Entry: "shared", // collision
				Exit:  "b",
				Nodes: []TemplateNodeDef{{Type: "shared"}, {Type: "b", Subscribes: []SubscriptionEntry{{Node: "shared", On: "state"}}}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "duplicate node type") {
		t.Fatalf("expected duplicate-node-type rejection; got: %v", msgs)
	}
}

// Markers — IsSubgraphEntryAbsorbed emitted on calling nodes; the
// canonicalizer flatten step sets the marker so the runtime
// supervisor's terminal handler can route through the sub-graph
// internal-cascade fire without a per-template lookup.
func TestCanonicalizeGraphs_EmitsIsSubgraphEntryAbsorbed(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "caller", Delegate: "sub"},
					{Type: "plain", Executor: "stub"},
				},
			},
			{
				Name:  "sub",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []TemplateNodeDef{
					{Type: "validate", Executor: "stub"},
					{Type: "promote", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "validate", On: "state"}}},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected validation errors: %v", res.Errors)
	}
	var caller, plain *TemplateNodeDef
	for i := range spec.Nodes {
		switch spec.Nodes[i].Type {
		case "caller":
			caller = &spec.Nodes[i]
		case "plain":
			plain = &spec.Nodes[i]
		}
	}
	if caller == nil || !caller.IsSubgraphEntryAbsorbed {
		t.Fatalf("caller node missing IsSubgraphEntryAbsorbed marker: %+v", caller)
	}
	if plain == nil || plain.IsSubgraphEntryAbsorbed {
		t.Fatalf("plain node should not carry IsSubgraphEntryAbsorbed: %+v", plain)
	}
}

// Markers — IsSubgraphExit emitted on the declared `exit:` of every
// non-main graph; the runtime terminal handler reads the marker via
// acq.NodeDef to drive the carry-rule without a per-terminal template
// lookup.
func TestCanonicalizeGraphs_EmitsIsSubgraphExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "caller", Delegate: "sub"},
					{Type: "plain", Executor: "stub"},
				},
			},
			{
				Name:  "sub",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []TemplateNodeDef{
					{Type: "validate", Executor: "stub"},
					{Type: "promote", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "validate", On: "state"}}},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected validation errors: %v", res.Errors)
	}
	byType := make(map[string]*TemplateNodeDef, len(spec.Nodes))
	for i := range spec.Nodes {
		byType[spec.Nodes[i].Type] = &spec.Nodes[i]
	}
	if exit := byType["promote"]; exit == nil || !exit.IsSubgraphExit {
		t.Fatalf("promote (sub-graph exit) must carry IsSubgraphExit: %+v", exit)
	}
	if entry := byType["validate"]; entry == nil || entry.IsSubgraphExit {
		t.Fatalf("validate (entry) must not carry IsSubgraphExit: %+v", entry)
	}
	if caller := byType["caller"]; caller == nil || caller.IsSubgraphExit {
		t.Fatalf("caller (in main) must not carry IsSubgraphExit: %+v", caller)
	}
	if plain := byType["plain"]; plain == nil || plain.IsSubgraphExit {
		t.Fatalf("plain (in main) must not carry IsSubgraphExit: %+v", plain)
	}
}

// Markers — flat-shape templates (no `graphs:` block, just top-level
// `nodes:`) must never set IsSubgraphExit, regardless of the type
// name. The marker is exclusive to non-main graphs' declared `exit:`
// nodes; a flat-shape template has no graphs and therefore no exits.
// A node accidentally named "exit" still falls under main and stays
// unmarked. Pins this so a future refactor of the canonicalizer can't
// silently enable IsSubgraphExit on flat-shape inputs.
func TestCanonicalizeGraphs_FlatShape_NoIsSubgraphExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl-flat",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Nodes: []TemplateNodeDef{
			{Type: "alpha", Executor: "stub"},
			{Type: "beta", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "alpha", On: "state"}}},
			// A node literally named "exit" — the marker is keyed on
			// `graphs[i].Exit`, not on the type name, so even this node
			// must stay unmarked under flat shape.
			{Type: "exit", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "beta", On: "state"}}},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected validation errors: %v", res.Errors)
	}
	for i := range spec.Nodes {
		if spec.Nodes[i].IsSubgraphExit {
			t.Fatalf("flat-shape node %q must not carry IsSubgraphExit: %+v",
				spec.Nodes[i].Type, spec.Nodes[i])
		}
	}
}

// Markers — ResolvesViaCallingNode emitted on subscription edges from
// non-entry internal nodes that reference the sub-graph's entry alias.
func TestCanonicalizeGraphs_EmitsResolvesViaCallingNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "caller", Delegate: "sub"},
				},
			},
			{
				Name:  "sub",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []TemplateNodeDef{
					{Type: "validate", Executor: "stub"},
					{Type: "transform", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "validate", On: "state"}}},
					{Type: "promote", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "transform", On: "state"}}},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected validation errors: %v", res.Errors)
	}
	var transform, promote *TemplateNodeDef
	for i := range spec.Nodes {
		switch spec.Nodes[i].Type {
		case "transform":
			transform = &spec.Nodes[i]
		case "promote":
			promote = &spec.Nodes[i]
		}
	}
	if transform == nil || len(transform.Subscribes) != 1 || !transform.Subscribes[0].ResolvesViaCallingNode {
		t.Fatalf("transform's subscribe to entry alias missing ResolvesViaCallingNode: %+v", transform)
	}
	// promote subscribes to transform (an interior internal), NOT the
	// entry alias — the marker should not be set on that edge.
	if promote == nil || len(promote.Subscribes) != 1 || promote.Subscribes[0].ResolvesViaCallingNode {
		t.Fatalf("promote subscribes to transform; should not carry ResolvesViaCallingNode: %+v", promote)
	}
}

// D2 mutual-exclusion under graphs shape: an author who declares BOTH
// `executor:` and `delegate:` on a calling node in the `graphs:` shape
// must be rejected — even when the sub-graph's entry node declares no
// executor of its own. The IsSubgraphEntryAbsorbed marker disables the
// flat-shape `validateExecutorCoherence` check, so the rejection has to
// fire from inside `absorbEntryIntoCaller` where it sees the author's
// original declaration.
func TestCanonicalizeGraphs_RejectCallerExecutorAndDelegate_EntryHasNoExecutor(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "tmpl",
		Version:             "1",
		FrameResolutionMode: FrameResolutionCoalesce,
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					// Author error: calling node carries both executor:
					// and delegate:. The entry node below has no
					// executor of its own, which previously slipped
					// past the diverging-executor merge guard.
					{Type: "caller", Executor: "stub", Delegate: "sub"},
				},
			},
			{
				Name:  "sub",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []TemplateNodeDef{
					{Type: "validate"},
					{Type: "promote", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "validate", On: "state"}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "delegate and executor are mutually exclusive") {
		t.Fatalf("expected mutual-exclusion rejection; got: %v", msgs)
	}
}
