// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N1 scenario — fanout_aggregation.
//
// Drives runtime.Aggregate against a fan-out parent's snapshot
// AggregationPolicy across the policy table. Mirrors the fanout/
// scenarios in spirit but lives under run_tree/ to keep the per-
// scenario directory layout the N1 brief requests.
package runtree

import (
	"testing"

	"github.com/fallguy/rimsky/foundation/cascade"
	tmplspec "github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/runtime"
)

// TestFanoutAggregation_PolicyTable iterates every declared policy
// kind and pins the parent's aggregated state for a representative
// child-outcome slice.
func TestFanoutAggregation_PolicyTable(t *testing.T) {
	t.Parallel()
	allFresh := []runtime.ChildState{
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
	}
	mixed := []runtime.ChildState{
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
		{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed},
	}
	cases := []struct {
		name      string
		policy    tmplspec.AggregationPolicy
		children  []runtime.ChildState
		wantTerm  bool
		wantState cascade.NodeState
	}{
		{"strict_all_success", tmplspec.AggregationPolicy{Kind: "strict"}, allFresh, true, cascade.NodeStateFresh},
		{"strict_any_failure", tmplspec.AggregationPolicy{Kind: "strict"}, mixed, true, cascade.NodeStateFailed},
		{"threshold_below_max", tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 5}, mixed, true, cascade.NodeStateFresh},
		{"threshold_at_max", tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 1}, mixed, true, cascade.NodeStateFailed},
		{"best_effort_tolerates_failures", tmplspec.AggregationPolicy{Kind: "best_effort"}, mixed, true, cascade.NodeStateFresh},
		{"first_picks_winner", tmplspec.AggregationPolicy{Kind: "first"}, mixed, true, cascade.NodeStateFresh},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := runtime.Aggregate(tc.children, tc.policy)
			if res.IsTerminal != tc.wantTerm {
				t.Fatalf("%s: terminal got %v want %v", tc.name, res.IsTerminal, tc.wantTerm)
			}
			if res.IsTerminal && res.ParentState != tc.wantState {
				t.Errorf("%s: parent state %s want %s", tc.name, res.ParentState, tc.wantState)
			}
		})
	}
}

// TestFanoutAggregation_EmptyChildrenStaysRunning pins the "no
// children yet" case: the engine returns IsTerminal=false so the
// parent stays in its current state (typically running).
func TestFanoutAggregation_EmptyChildrenStaysRunning(t *testing.T) {
	t.Parallel()
	res := runtime.Aggregate(nil, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.IsTerminal {
		t.Errorf("empty children: parent must stay non-terminal; got %s", res.ParentState)
	}
}

// TestFanoutAggregation_UnknownPolicyFallsBackToStrict pins the
// safety default: unrecognized policy kinds fall back to strict.
func TestFanoutAggregation_UnknownPolicyFallsBackToStrict(t *testing.T) {
	t.Parallel()
	mixed := []runtime.ChildState{
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
		{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed},
	}
	res := runtime.Aggregate(mixed, tmplspec.AggregationPolicy{Kind: "some-future-policy"})
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("unknown policy fallback: parent state %s (want strict→failed)", res.ParentState)
	}
}
