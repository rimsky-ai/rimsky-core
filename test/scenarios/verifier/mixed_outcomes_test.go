// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N10 scenario — mixed_outcomes.
//
// A verifier-pattern subgraph may have multiple verifier co-holders;
// the supervisor's aggregation engine resolves the parent's
// terminal per the snapshot policy. Mixed outcomes (some pass, some
// fail) interact with the parent's error_policy: strict fails,
// best_effort succeeds, threshold checks the count.
package verifier

import (
	"testing"

	"github.com/fallguy/rimsky/foundation/cascade"
	tmplspec "github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/runtime"
)

func TestMixedOutcomes_StrictAnyFailFails(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged}, // verifier #1 passed
		{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed},      // verifier #2 failed
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFailed {
		t.Errorf("strict mixed: expected parent failed, got terminal=%v state=%s",
			res.IsTerminal, res.ParentState)
	}
}

func TestMixedOutcomes_BestEffortStillCommits(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
		{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "best_effort"})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFresh {
		t.Errorf("best_effort mixed: expected parent fresh, got terminal=%v state=%s",
			res.IsTerminal, res.ParentState)
	}
}

func TestMixedOutcomes_ThresholdGuardsCount(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, LastOutcome: cascade.LastOutcomeFreshChanged},
		{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed},
		{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed},
	}
	// threshold max_failures=2: at-max → failed.
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFailed {
		t.Errorf("threshold at-max: expected failed, got %s", res.ParentState)
	}
	// threshold max_failures=5: below-max → success.
	res = runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 5})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFresh {
		t.Errorf("threshold below-max: expected fresh, got %s", res.ParentState)
	}
}
