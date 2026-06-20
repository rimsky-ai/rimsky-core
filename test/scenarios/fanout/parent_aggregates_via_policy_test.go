// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package fanout

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestParentAggregatesViaPolicy_StrictFailsOnAnyFailure(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict", CancelSiblings: true})
	if !res.IsSettled {
		t.Fatalf("strict aggregation should settle on any-failed; got non-terminal")
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("strict parent state: %s (want failed)", res.ParentState)
	}
	if res.Action != runtime.AggregateActionCancelSiblings {
		t.Errorf("strict.cancel_siblings should request sibling cancellation; got %v", res.Action)
	}
}

func TestParentAggregatesViaPolicy_StrictAllSuccessYieldsFreshChanged(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Fatalf("strict aggregation should settle when every child is terminal")
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Errorf("strict parent state: %s (want fresh)", res.ParentState)
	}
	if !res.ParentChanged {
		t.Errorf("strict parent outcome: %s (want fresh_changed when any child reported it)", res.ParentSettlingSignalType)
	}
}

func TestParentAggregatesViaPolicy_ThresholdTolerates(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsSettled {
		t.Fatalf("threshold should settle once all children are terminal")
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Errorf("threshold tolerates failures < max; got state %s", res.ParentState)
	}
}

func TestParentAggregatesViaPolicy_BestEffortIgnoresFailures(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "best_effort"})
	if !res.IsSettled {
		t.Fatalf("best_effort should settle once all children terminate")
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Errorf("best_effort parent state: %s (want fresh)", res.ParentState)
	}
}

func TestParentAggregatesViaPolicy_FirstCancelsNonWinners(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateRunning},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateRunning},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "first"})
	if !res.IsSettled {
		t.Fatalf("first should settle on the first success")
	}
	if res.Action != runtime.AggregateActionCancelNonWinners {
		t.Errorf("first should cancel non-winners; got %v", res.Action)
	}
}

func TestFanOutAggregationPolicy_ReadsTemplateBlock(t *testing.T) {
	t.Parallel()
	def := &node.TemplateNodeDef{
		Type: "per-region-loader",
		FanOut: &tmplspec.FanOutSpec{
			Claim:            "data",
			PartitionRequest: `{"kind":"region-list"}`,
			ErrorPolicy:      tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 2},
		},
	}
	got := runtime.FanOutAggregationPolicy(def)
	if got.Kind != "threshold" || got.MaxFailures != 2 {
		t.Errorf("FanOutAggregationPolicy: %+v (want threshold/MaxFailures=2)", got)
	}
}
