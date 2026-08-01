// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestValidate_RejectsAuthorSetIsSubgraphEntryAbsorbed_FlatForm(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "sneaky", Executor: "stub", Delegate: "sub", IsSubgraphEntryAbsorbed: true},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "is_subgraph_entry_absorbed is set by subgraph canonicalization"),
		"expected rejection of author-set is_subgraph_entry_absorbed (executor/delegate bypass closed); got: %v", msgs)
}

func TestValidate_RejectsAuthorSetIsSubgraphExit_FlatForm(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "sneaky", Executor: "stub", IsSubgraphExit: true},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "is_subgraph_exit is set by subgraph canonicalization"),
		"expected rejection of author-set is_subgraph_exit; got: %v", msgs)
}

func TestValidate_RejectsAuthorSetResolvesViaCallingNode_FlatForm(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "sneaky", Executor: "stub", Subscribes: []SubscriptionEntry{
				{Node: "other", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false), ResolvesViaCallingNode: true},
			}},
			{Type: "other", Executor: "stub"},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "resolves_via_calling_node is set by subgraph canonicalization"),
		"expected rejection of author-set resolves_via_calling_node; got: %v", msgs)
}

func TestValidate_RejectsAuthorSetInternalFlag_GraphsForm(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "alpha", Executor: "stub", IsSubgraphEntryAbsorbed: true},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "is_subgraph_entry_absorbed is set by subgraph canonicalization"),
		"expected rejection of author-set flag in graphs-form node; got: %v", msgs)
}

func TestCanonicalizeGraphs_HappyPathSingleMain(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl-1",
		Version: "1",
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
	require.Empty(t, msgs, "expected no errors; got: %v", msgs)
	require.Len(t, spec.Nodes, 1)
	require.Equal(t, "alpha", spec.Nodes[0].Type, "expected canonicalizer to flatten Nodes to [alpha], got: %+v", spec.Nodes)
}

func TestCanonicalizeGraphs_WhitespacePaddedMainNameTreatedAsMain(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl-1",
		Version: "1",
		Graphs: []GraphSpec{
			{
				Name: MainGraphName + " ",
				Nodes: []TemplateNodeDef{
					{Type: "alpha", Delegate: "sub"},
				},
			},
			{
				Name:  "sub",
				Entry: "b",
				Exit:  "c",
				Nodes: []TemplateNodeDef{
					{Type: "b"},
					{Type: "c", Subscribes: []SubscriptionEntry{{Node: "b", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if hasErrorContaining(msgs, "subgraph_missing_main") {
		t.Fatalf("whitespace-padded main graph name must still satisfy the missing-main gate; got: %v", msgs)
	}
	if hasErrorContaining(msgs, "subgraph_missing_entry") || hasErrorContaining(msgs, "subgraph_missing_exit") {
		t.Fatalf("whitespace-padded main graph name must be treated as the main graph, not a phantom sub-graph; got: %v", msgs)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected no errors; got: %v", msgs)
	}
}

func TestCanonicalizeGraphs_RejectGraphsAndNodesBothSet(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Nodes:   []TemplateNodeDef{{Type: "x"}},
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "y"}}},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "graphs_and_nodes_both_set"),
		"expected graphs_and_nodes_both_set rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectMissingMain(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
	require.True(t, hasErrorContaining(msgs, "subgraph_missing_main"),
		"expected subgraph_missing_main rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectMainHasEntryExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{
				Name:  MainGraphName,
				Entry: "a",
				Nodes: []TemplateNodeDef{{Type: "a"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "subgraph_main_has_entry_or_exit"),
		"expected subgraph_main_has_entry_or_exit rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectSubGraphMissingEntry(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
	require.True(t, hasErrorContaining(msgs, "subgraph_missing_entry"),
		"expected subgraph_missing_entry rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectSubGraphMissingExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
	require.True(t, hasErrorContaining(msgs, "subgraph_missing_exit"),
		"expected subgraph_missing_exit rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectEntryEqualsExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
	require.True(t, hasErrorContaining(msgs, "subgraph_entry_equals_exit"),
		"expected subgraph_entry_equals_exit rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectUnknownExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "ghost",
				Nodes: []TemplateNodeDef{{Type: "a"}, {Type: "b"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "subgraph_unknown_exit"),
		"expected subgraph_unknown_exit rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectDuplicateGraphName(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "b",
				Nodes: []TemplateNodeDef{{Type: "a"}, {Type: "b"}},
			},
			{
				Name:  "sub",
				Entry: "c",
				Exit:  "d",
				Nodes: []TemplateNodeDef{{Type: "c"}, {Type: "d"}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "duplicate graph name"),
		"expected duplicate graph name rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectUnknownEntry(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
	require.True(t, hasErrorContaining(msgs, "subgraph_unknown_entry"),
		"expected subgraph_unknown_entry rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectDisconnectedInternalNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "z",
				Nodes: []TemplateNodeDef{
					{Type: "a"},
					{Type: "orphan"},
					{Type: "z", Subscribes: []SubscriptionEntry{{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "subgraph_disconnected_internal_node"),
		"expected subgraph_disconnected_internal_node; got: %v", msgs)
}

func TestCanonicalizeGraphs_ReachabilityHappyPath(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "m"}}},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "c",
				Nodes: []TemplateNodeDef{
					{Type: "a"},
					{Type: "b", Subscribes: []SubscriptionEntry{{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
					{Type: "c", Subscribes: []SubscriptionEntry{{Node: "b", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.False(t, hasErrorContaining(msgs, "subgraph_disconnected_internal_node"),
		"expected no disconnect; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectDelegateCycle(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
					{Type: "g1x", Subscribes: []SubscriptionEntry{{Node: "g1n", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
			{
				Name:  "g2",
				Entry: "g2n",
				Exit:  "g2x",
				Nodes: []TemplateNodeDef{
					{Type: "g2n", Delegate: "g1"},
					{Type: "g2x", Subscribes: []SubscriptionEntry{{Node: "g2n", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "subgraph_recursion_unsupported"),
		"expected subgraph_recursion_unsupported; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectInternalReferencesOuter(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
					{Type: "b", Subscribes: []SubscriptionEntry{{Node: "outer", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "subgraph_internal_references_outer"),
		"expected subgraph_internal_references_outer; got: %v", msgs)
}

func TestCanonicalizeGraphs_RejectOuterSubscribesToInternal(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "outer", Subscribes: []SubscriptionEntry{{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "b",
				Nodes: []TemplateNodeDef{
					{Type: "a"},
					{Type: "b", Subscribes: []SubscriptionEntry{{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_outer_references_internal") {
		t.Fatalf("expected subgraph_outer_references_internal; got: %v", msgs)
	}
}

func TestCanonicalizeGraphs_RejectOuterHoldsFromInternal(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "outer", Holds: map[string]HoldsBinding{"x": {From: "a"}}},
				},
			},
			{
				Name:  "sub",
				Entry: "a",
				Exit:  "b",
				Nodes: []TemplateNodeDef{
					{Type: "a"},
					{Type: "b", Subscribes: []SubscriptionEntry{{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	if !hasErrorContaining(msgs, "subgraph_outer_references_internal") {
		t.Fatalf("expected subgraph_outer_references_internal; got: %v", msgs)
	}
}

func TestCanonicalizeGraphs_RejectDuplicateNodeTypeAcrossGraphs(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "shared"}}},
			{
				Name:  "sub",
				Entry: "shared",
				Exit:  "b",
				Nodes: []TemplateNodeDef{{Type: "shared"}, {Type: "b", Subscribes: []SubscriptionEntry{{Node: "shared", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}}},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "duplicate node type"),
		"expected duplicate-node-type rejection; got: %v", msgs)
}

func TestCanonicalizeGraphs_EmitsIsSubgraphEntryAbsorbed(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
					{Type: "promote", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "validate", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.Empty(t, res.Errors, "unexpected validation errors: %v", res.Errors)
	var caller, plain *TemplateNodeDef
	for i := range spec.Nodes {
		switch spec.Nodes[i].Type {
		case "caller":
			caller = &spec.Nodes[i]
		case "plain":
			plain = &spec.Nodes[i]
		}
	}
	require.NotNil(t, caller)
	require.True(t, caller.IsSubgraphEntryAbsorbed, "caller node missing IsSubgraphEntryAbsorbed marker: %+v", caller)
	require.NotNil(t, plain)
	require.False(t, plain.IsSubgraphEntryAbsorbed, "plain node should not carry IsSubgraphEntryAbsorbed: %+v", plain)
	require.Equal(t, "stub", caller.Executor,
		"absorbed caller must inherit the entry's executor (delegation.md: 'the calling node's executor is taken from the entry')")
}

func TestCanonicalizeGraphs_EmitsIsSubgraphExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
					{Type: "promote", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "validate", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.Empty(t, res.Errors, "unexpected validation errors: %v", res.Errors)
	byType := make(map[string]*TemplateNodeDef, len(spec.Nodes))
	for i := range spec.Nodes {
		byType[spec.Nodes[i].Type] = &spec.Nodes[i]
	}
	exit := byType["promote"]
	require.NotNil(t, exit)
	require.True(t, exit.IsSubgraphExit, "promote (sub-graph exit) must carry IsSubgraphExit: %+v", exit)
	entry := byType["validate"]
	require.NotNil(t, entry)
	require.False(t, entry.IsSubgraphExit, "validate (entry) must not carry IsSubgraphExit: %+v", entry)
	caller := byType["caller"]
	require.NotNil(t, caller)
	require.False(t, caller.IsSubgraphExit, "caller (in main) must not carry IsSubgraphExit: %+v", caller)
	plain := byType["plain"]
	require.NotNil(t, plain)
	require.False(t, plain.IsSubgraphExit, "plain (in main) must not carry IsSubgraphExit: %+v", plain)
}

func TestCanonicalizeGraphs_FlatShape_NoIsSubgraphExit(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl-flat",
		Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "alpha", Executor: "stub"},
			{Type: "beta", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "alpha", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
			{Type: "exit", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "beta", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.Empty(t, res.Errors, "unexpected validation errors: %v", res.Errors)
	for i := range spec.Nodes {
		require.False(t, spec.Nodes[i].IsSubgraphExit,
			"flat-shape node %q must not carry IsSubgraphExit: %+v", spec.Nodes[i].Type, spec.Nodes[i])
	}
}

func TestCanonicalizeGraphs_EmitsResolvesViaCallingNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
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
					{Type: "transform", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "validate", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
					{Type: "promote", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "transform", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.Empty(t, res.Errors, "unexpected validation errors: %v", res.Errors)
	var transform, promote *TemplateNodeDef
	for i := range spec.Nodes {
		switch spec.Nodes[i].Type {
		case "transform":
			transform = &spec.Nodes[i]
		case "promote":
			promote = &spec.Nodes[i]
		}
	}
	require.NotNil(t, transform)
	require.Len(t, transform.Subscribes, 1)
	require.True(t, transform.Subscribes[0].ResolvesViaCallingNode,
		"transform's subscribe to entry alias missing ResolvesViaCallingNode: %+v", transform)
	require.NotNil(t, promote)
	require.Len(t, promote.Subscribes, 1)
	require.False(t, promote.Subscribes[0].ResolvesViaCallingNode,
		"promote subscribes to transform; should not carry ResolvesViaCallingNode: %+v", promote)
}

func TestCanonicalizeGraphs_RejectCallerExecutorAndDelegate_EntryHasNoExecutor(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "tmpl",
		Version: "1",
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{Type: "caller", Executor: "stub", Delegate: "sub"},
				},
			},
			{
				Name:  "sub",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []TemplateNodeDef{
					{Type: "validate"},
					{Type: "promote", Executor: "stub", Subscribes: []SubscriptionEntry{{Node: "validate", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
				},
			},
		},
	}
	msgs := validateMultiGraph(t, spec)
	require.True(t, hasErrorContaining(msgs, "delegate and executor are mutually exclusive"),
		"expected mutual-exclusion rejection; got: %v", msgs)
}

func TestAbsorbEntryIntoCaller_ExecutorInheritedFromEntry(t *testing.T) {
	caller := TemplateNodeDef{Type: "caller", Delegate: "sub"}
	entry := TemplateNodeDef{Type: "entry_node", Executor: "handler.entry"}
	out, errs := absorbEntryIntoCaller(caller, entry, "graphs[0].nodes[0]")
	require.Empty(t, errs, "unexpected errors: %+v", errs)
	require.Equal(t, "handler.entry", out.Executor,
		"expected caller to inherit entry's executor when caller declares none")
}

func TestAbsorbEntryIntoCaller_CallerOwnExecutorAlwaysRejected(t *testing.T) {
	cases := []struct {
		name           string
		callerExecutor string
		entryExecutor  string
	}{
		{"diverging executors", "handler.caller", "handler.entry"},
		{"identical executors — still rejected, no carve-out", "handler.shared", "handler.shared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller := TemplateNodeDef{Type: "caller", Executor: tc.callerExecutor, Delegate: "sub"}
			entry := TemplateNodeDef{Type: "entry_node", Executor: tc.entryExecutor}
			out, errs := absorbEntryIntoCaller(caller, entry, "graphs[0].nodes[0]")
			require.NotEmpty(t, errs,
				"expected rejection: a delegating caller must never declare its own executor (delegation.md: 'declaring both is rejected'); out=%+v", out)
			require.Contains(t, errs[0].Msg, "delegate and executor are mutually exclusive")
		})
	}
}

func TestMergeClaimProducersOnAbsorb_IdenticalAliasMerges(t *testing.T) {
	callerProducers := []NodeClaimProducerRef{
		{Name: "content", Alias: "shared", Intent: "rw", Selector: "{{params.a}}"},
	}
	entryProducers := []NodeClaimProducerRef{
		{Name: "content", Alias: "shared", Intent: "rw", Selector: "{{params.a}}"},
	}
	merged, errs := mergeClaimProducersOnAbsorb(callerProducers, entryProducers, "graphs[0].nodes[0]")
	require.Empty(t, errs, "unexpected errors merging identical claim producer aliases: %+v", errs)
	require.Len(t, merged, 1, "expected identical alias to dedup to one entry, got %+v", merged)
}

func TestMergeClaimProducersOnAbsorb_DivergingAliasRejected(t *testing.T) {
	callerProducers := []NodeClaimProducerRef{
		{Name: "content", Alias: "shared", Intent: "rw", Selector: "{{params.a}}"},
	}
	entryProducers := []NodeClaimProducerRef{
		{Name: "other", Alias: "shared", Intent: "rw", Selector: "{{params.b}}"},
	}
	merged, errs := mergeClaimProducersOnAbsorb(callerProducers, entryProducers, "graphs[0].nodes[0]")
	require.NotEmpty(t, errs, "expected subgraph_absorption_alias_conflict for diverging claim producer bindings, merged=%+v", merged)
	require.Contains(t, errs[0].Msg, "subgraph_absorption_alias_conflict")
}

func TestMergeHoldsOnAbsorb_IdenticalAliasMerges(t *testing.T) {
	callerHolds := map[string]HoldsBinding{"x": {From: "producer"}}
	entryHolds := map[string]HoldsBinding{"x": {From: "producer"}}
	merged, errs := mergeHoldsOnAbsorb(callerHolds, entryHolds, "graphs[0].nodes[0]")
	require.Empty(t, errs, "unexpected errors merging identical holds aliases: %+v", errs)
	require.Len(t, merged, 1)
	require.Equal(t, "producer", merged["x"].From,
		"expected merged holds to contain one entry x->producer, got %+v", merged)
}

func TestMergeHoldsOnAbsorb_DivergingAliasRejected(t *testing.T) {
	callerHolds := map[string]HoldsBinding{"x": {From: "producer_a"}}
	entryHolds := map[string]HoldsBinding{"x": {From: "producer_b"}}
	merged, errs := mergeHoldsOnAbsorb(callerHolds, entryHolds, "graphs[0].nodes[0]")
	require.NotEmpty(t, errs, "expected subgraph_absorption_alias_conflict for diverging holds bindings, merged=%+v", merged)
	require.Contains(t, errs[0].Msg, "subgraph_absorption_alias_conflict")
}

func TestAbsorbEntryIntoCaller_AttributeSchemaDeepMerges(t *testing.T) {
	caller := TemplateNodeDef{
		Type:     "caller",
		Delegate: "sub",
		Attributes: &NodeAttributesDef{Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"caller_field": map[string]any{"type": "string", "default": "c"},
			},
		}},
	}
	entry := TemplateNodeDef{
		Type: "entry_node",
		Attributes: &NodeAttributesDef{Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"entry_field": map[string]any{"type": "string", "default": "e"},
			},
		}},
	}
	out, errs := absorbEntryIntoCaller(caller, entry, "graphs[0].nodes[0]")
	require.Empty(t, errs, "unexpected errors: %+v", errs)
	require.NotNil(t, out.Attributes, "expected merged attributes schema, got nil")
	props, _ := out.Attributes.Schema["properties"].(map[string]any)
	require.Contains(t, props, "caller_field", "merged schema missing caller_field: %+v", props)
	require.Contains(t, props, "entry_field", "merged schema missing entry_field: %+v", props)
}
