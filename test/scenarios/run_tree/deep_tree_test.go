// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N1 scenario — deep_tree_subgraph_of_fanout / deep_tree_fanout_of_subgraph.
//
// Both deep-tree cases reduce to a recursive Aggregate at each
// level. The check pins that nesting Aggregate (outer-policy over a
// child whose state was itself produced by an inner Aggregate)
// terminates correctly and propagates the inner verdict to the
// outer.
package runtree

import (
	"testing"

	"github.com/fallguy/rimsky/foundation/cascade"
	tmplspec "github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/runtime"
)

// TestDeepTree_SubgraphOfFanout: outer is a fan-out (strict);
// inner is a sub-graph (no policy — sub-graph internals aggregate
// via per-graph rules but at the outer level we observe a single
// child per fan-out partition). Models: each fan-out leaf was
// itself a sub-graph that fully terminated → the leaf reports a
// single ChildState upward, which the outer aggregates via its
// fan-out policy.
func TestDeepTree_SubgraphOfFanout(t *testing.T) {
	t.Parallel()
	// Two fan-out partitions; each partition's sub-graph terminated
	// fresh_changed.
	innerVerdicts := []runtime.ChildState{
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
	}
	outer := runtime.Aggregate(innerVerdicts, tmplspec.AggregationPolicy{Kind: "strict"})
	if !outer.IsTerminal {
		t.Fatal("outer fan-out should settle once each sub-graph terminated")
	}
	if outer.ParentState != cascade.NodeStateFresh {
		t.Errorf("outer state: %s (want fresh)", outer.ParentState)
	}
	if outer.ParentOutcome != cascade.LastOutcomeFreshChanged {
		t.Errorf("outer outcome: %s (want fresh_changed — cascade propagates)", outer.ParentOutcome)
	}
}

// TestDeepTree_SubgraphOfFanoutOneInnerFails: when one inner
// sub-graph reports failed, the outer fan-out with strict policy
// surfaces failed.
func TestDeepTree_SubgraphOfFanoutOneInnerFails(t *testing.T) {
	t.Parallel()
	innerVerdicts := []runtime.ChildState{
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
		{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed},
	}
	outer := runtime.Aggregate(innerVerdicts, tmplspec.AggregationPolicy{Kind: "strict"})
	if !outer.IsTerminal {
		t.Fatal("outer fan-out should settle when any inner reports failed")
	}
	if outer.ParentState != cascade.NodeStateFailed {
		t.Errorf("outer state: %s (want failed)", outer.ParentState)
	}
}

// TestDeepTree_FanoutOfSubgraph: outer is a sub-graph (single-
// chunk caller); the inner is a fan-out whose aggregated verdict
// propagates upward. The sub-graph's calling node carries the
// single ChildState upward.
func TestDeepTree_FanoutOfSubgraph(t *testing.T) {
	t.Parallel()
	// Inner fan-out had a mixed outcome but `best_effort` policy
	// produced a successful inner verdict.
	innerChildren := []runtime.ChildState{
		{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed},
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
	}
	innerVerdict := runtime.Aggregate(innerChildren, tmplspec.AggregationPolicy{Kind: "best_effort"})
	if !innerVerdict.IsTerminal {
		t.Fatal("inner fan-out should settle")
	}
	if innerVerdict.ParentState != cascade.NodeStateFresh {
		t.Errorf("inner best_effort verdict: %s (want fresh)", innerVerdict.ParentState)
	}
	// Outer sub-graph: a single ChildState carries the inner
	// fan-out's settled verdict upward.
	outer := runtime.Aggregate([]runtime.ChildState{
		{State: innerVerdict.ParentState, LastOutcome: innerVerdict.ParentOutcome},
	}, tmplspec.AggregationPolicy{Kind: "strict"})
	if !outer.IsTerminal {
		t.Fatal("outer sub-graph should settle on inner verdict")
	}
	if outer.ParentState != cascade.NodeStateFresh {
		t.Errorf("outer state: %s (want fresh)", outer.ParentState)
	}
}
