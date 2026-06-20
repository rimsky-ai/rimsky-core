// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package verifier

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestMixedOutcomes_StrictAnyFailFails(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFailed {
		t.Errorf("strict mixed: expected parent failed, got terminal=%v state=%s",
			res.IsSettled, res.ParentState)
	}
}

func TestMixedOutcomes_BestEffortStillCommits(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "best_effort"})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFresh {
		t.Errorf("best_effort mixed: expected parent fresh, got terminal=%v state=%s",
			res.IsSettled, res.ParentState)
	}
}

func TestMixedOutcomes_ThresholdGuardsCount(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFailed {
		t.Errorf("threshold at-max: expected failed, got %s", res.ParentState)
	}
	res = runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 5})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFresh {
		t.Errorf("threshold below-max: expected fresh, got %s", res.ParentState)
	}
}
