// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/spec"
)

func success(outcome cascade.LastOutcome) ChildState {
	return ChildState{State: cascade.NodeStateFresh, LastOutcome: outcome}
}

func failure() ChildState {
	return ChildState{State: cascade.NodeStateFailed, LastOutcome: cascade.LastOutcomeFailed}
}

func running() ChildState {
	return ChildState{State: cascade.NodeStateRunning, LastOutcome: cascade.LastOutcomeFreshUnchanged}
}

// TestAggregate_StrictAllSuccess — all children settle successfully under
// the default strict policy: parent → fresh + fresh_unchanged.
func TestAggregate_StrictAllSuccess(t *testing.T) {
	children := []ChildState{
		success(cascade.LastOutcomeFreshUnchanged),
		success(cascade.LastOutcomeFreshUnchanged),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if !res.IsTerminal {
		t.Fatalf("expected terminal; got %+v", res)
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Fatalf("expected fresh; got %q", res.ParentState)
	}
	if res.ParentOutcome != cascade.LastOutcomeFreshUnchanged {
		t.Fatalf("expected fresh_unchanged; got %q", res.ParentOutcome)
	}
}

// TestAggregate_StrictAnyChange — at least one child reports
// fresh_changed → parent reports fresh_changed (cascade-firing gate).
func TestAggregate_StrictAnyChange(t *testing.T) {
	children := []ChildState{
		success(cascade.LastOutcomeFreshUnchanged),
		success(cascade.LastOutcomeFreshChanged),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if !res.IsTerminal || res.ParentOutcome != cascade.LastOutcomeFreshChanged {
		t.Fatalf("expected fresh_changed; got %+v", res)
	}
}

// TestAggregate_StrictAnyFailure — strict policy short-circuits to
// failed on the first failed child.
func TestAggregate_StrictAnyFailure(t *testing.T) {
	children := []ChildState{
		success(cascade.LastOutcomeFreshChanged),
		failure(),
		running(),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if !res.IsTerminal {
		t.Fatalf("expected terminal; got %+v", res)
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected failed; got %q", res.ParentState)
	}
	if res.Action != AggregateActionNone {
		t.Fatalf("expected no action without cancel_siblings; got %v", res.Action)
	}
}

// TestAggregate_StrictCancelSiblings — strict.cancel_siblings sets the
// cancel-siblings follow-up action.
func TestAggregate_StrictCancelSiblings(t *testing.T) {
	children := []ChildState{failure(), running()}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict", CancelSiblings: true})
	if !res.IsTerminal || res.Action != AggregateActionCancelSiblings {
		t.Fatalf("expected cancel-siblings; got %+v", res)
	}
}

// TestAggregate_StrictActiveBlocks — strict policy stays
// non-terminal while any child is still running / stale.
func TestAggregate_StrictActiveBlocks(t *testing.T) {
	children := []ChildState{
		success(cascade.LastOutcomeFreshUnchanged),
		running(),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if res.IsTerminal {
		t.Fatalf("expected non-terminal; got %+v", res)
	}
}

// TestAggregate_Threshold — threshold below max_failures → success.
func TestAggregate_ThresholdBelowMax(t *testing.T) {
	children := []ChildState{
		success(cascade.LastOutcomeFreshChanged),
		failure(),
		success(cascade.LastOutcomeFreshUnchanged),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFresh {
		t.Fatalf("expected fresh under threshold; got %+v", res)
	}
}

// TestAggregate_ThresholdAtMax — failures ≥ max → failed.
func TestAggregate_ThresholdAtMax(t *testing.T) {
	children := []ChildState{failure(), failure(), success(cascade.LastOutcomeFreshChanged)}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected failed at threshold; got %+v", res)
	}
}

// TestAggregate_BestEffort — best_effort accepts any number of failures;
// settles when all children terminal.
func TestAggregate_BestEffort(t *testing.T) {
	children := []ChildState{
		failure(),
		failure(),
		success(cascade.LastOutcomeFreshChanged),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "best_effort"})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFresh {
		t.Fatalf("expected fresh under best_effort; got %+v", res)
	}
	if res.ParentOutcome != cascade.LastOutcomeFreshChanged {
		t.Fatalf("expected fresh_changed outcome aggregation; got %q", res.ParentOutcome)
	}
}

// TestAggregate_FirstWinner — first success terminates with cancel-non-winners.
func TestAggregate_FirstWinner(t *testing.T) {
	children := []ChildState{
		running(),
		success(cascade.LastOutcomeFreshChanged),
		failure(),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "first"})
	if !res.IsTerminal || res.Action != AggregateActionCancelNonWinners {
		t.Fatalf("expected first-winner with cancel-non-winners; got %+v", res)
	}
}

// TestAggregate_FirstAllFailed — first policy degrades to failed when
// every child fails before a winner emerges.
func TestAggregate_FirstAllFailed(t *testing.T) {
	children := []ChildState{failure(), failure()}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "first"})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected failed; got %+v", res)
	}
}

// TestAggregate_NoChildren — parent stays non-terminal when there are
// no children yet.
func TestAggregate_NoChildren(t *testing.T) {
	res := Aggregate(nil, spec.AggregationPolicy{Kind: "strict"})
	if res.IsTerminal {
		t.Fatalf("expected non-terminal with no children; got %+v", res)
	}
}

// TestAggregate_DefaultPolicyKind — empty kind defaults to strict.
func TestAggregate_DefaultPolicyKind(t *testing.T) {
	children := []ChildState{failure()}
	res := Aggregate(children, spec.AggregationPolicy{})
	if !res.IsTerminal || res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected strict default → failed; got %+v", res)
	}
}
