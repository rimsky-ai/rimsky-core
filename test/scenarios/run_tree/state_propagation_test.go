// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N1 scenario — state_propagation.
//
// Exercises `runtime.Aggregate` over the run-tree's per-child
// summaries. The state-propagation engine walks the run-tree upward
// at every child terminal and recomputes the parent's state per its
// snapshot policy. Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §State aggregation rules for run-trees.
package runtree

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// TestStatePropagation_NonTerminalChildHoldsParent pins that a
// parent stays non-terminal while at least one child is still
// running / stale, regardless of any settled children's verdicts.
func TestStatePropagation_NonTerminalChildHoldsParent(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateRunning},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Errorf("parent must stay non-terminal while a child is running; got terminal=%s", res.ParentState)
	}
}

// TestStatePropagation_AllStaleStillNonTerminal asserts the engine
// distinguishes stale (non-terminal) from fresh/failed (terminal).
func TestStatePropagation_AllStaleStillNonTerminal(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateStale},
		{State: cascade.NodeStateStale},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Errorf("all-stale children: parent must stay non-terminal; got %s", res.ParentState)
	}
}

// @deliberate: TestStatePropagation_ParkedDoesNotPropagate pins the
// parked-cascade behavior: a parked child is non-terminal from the
// aggregation engine's perspective (cascade does not propagate from
// parked, but the run-tree aggregation engine specifically TREATS
// parked as a terminal state — distinct concerns).
//
// Per the runtime helper: `ChildState.IsSettled()` returns true on
// parked. The aggregation engine therefore settles the parent when
// all children are parked + fresh. The N1 contract is that the
// state-propagation engine doesn't crash or stall on parked
// children.
func TestStatePropagation_ParkedChildAggregatesAsTerminal(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateParked},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Errorf("parked child should be treated as terminal by Aggregate; got non-terminal")
	}
}

// TestStatePropagation_FreshUnchangedAggregatesToParent pins the
// pure-cascade outcome when every child reports fresh_unchanged: the
// parent's outcome is also unchanged.
func TestStatePropagation_FreshUnchangedAggregatesToParent(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Fatal("all-fresh children should settle the parent")
	}
	if res.ParentChanged {
		t.Errorf("all-unchanged children: parent outcome %s (want fresh_unchanged)", res.ParentSettlingSignalType)
	}
}

// TestStatePropagation_FreshChangedDominatesUnchanged pins the rule:
// when any child is fresh_changed, the parent's aggregated outcome
// is fresh_changed (so cascade fires from the parent).
func TestStatePropagation_FreshChangedDominatesUnchanged(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Fatal("all-fresh children should settle the parent")
	}
	if !res.ParentChanged {
		t.Errorf("any-changed child: parent outcome %s (want fresh_changed)", res.ParentSettlingSignalType)
	}
}
