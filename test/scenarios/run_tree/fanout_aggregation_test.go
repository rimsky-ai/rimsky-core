// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtree

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestFanoutAggregation_PolicyTable(t *testing.T) {
	t.Parallel()
	allFresh := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	mixed := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
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
			if res.IsSettled != tc.wantTerm {
				t.Fatalf("%s: terminal got %v want %v", tc.name, res.IsSettled, tc.wantTerm)
			}
			if res.IsSettled && res.ParentState != tc.wantState {
				t.Errorf("%s: parent state %s want %s", tc.name, res.ParentState, tc.wantState)
			}
		})
	}
}

func TestFanoutAggregation_EmptyChildrenStaysRunning(t *testing.T) {
	t.Parallel()
	res := runtime.Aggregate(nil, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Errorf("empty children: parent must stay non-terminal; got %s", res.ParentState)
	}
}

func TestFanoutAggregation_UnknownPolicyFallsBackToStrict(t *testing.T) {
	t.Parallel()
	mixed := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(mixed, tmplspec.AggregationPolicy{Kind: "some-future-policy"})
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("unknown policy fallback: parent state %s (want strict→failed)", res.ParentState)
	}
}
