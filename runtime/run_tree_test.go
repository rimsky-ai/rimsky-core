// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/foundation/spec"
)

// success constructs a child that settled terminal/success with the
// given `changed` projection. Mirrors the pre-Pass-5 `success(outcome)`
// helper: `changed=true` was `fresh_changed`, `changed=false` was
// `fresh_unchanged`.
func success(changed bool) ChildState {
	return ChildState{
		State:              cascade.NodeStateFresh,
		SettlingSignalType: signalpkg.TypePath("terminal/success"),
		Changed:            changed,
	}
}

func failure() ChildState {
	return ChildState{
		State:              cascade.NodeStateFailed,
		SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure"),
	}
}

func running() ChildState {
	return ChildState{State: cascade.NodeStateRunning}
}

// TestAggregate_StrictAllSuccess — all children settle successfully under
// the default strict policy: parent → fresh + terminal/success with
// changed=false (no child reported change).
func TestAggregate_StrictAllSuccess(t *testing.T) {
	children := []ChildState{
		success(false),
		success(false),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Fatalf("expected settled; got %+v", res)
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Fatalf("expected fresh; got %q", res.ParentState)
	}
	if res.ParentSettlingSignalType != signalpkg.TypePath("terminal/success") {
		t.Fatalf("expected terminal/success; got %q", res.ParentSettlingSignalType)
	}
	if res.ParentChanged {
		t.Fatalf("expected changed=false; got %+v", res)
	}
}

// TestAggregate_StrictAnyChange — at least one child reports
// changed=true → parent reports changed=true (the cascade-firing
// projection downstream subscribers gate on with `when: payload.changed`).
func TestAggregate_StrictAnyChange(t *testing.T) {
	children := []ChildState{
		success(false),
		success(true),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled || !res.ParentChanged {
		t.Fatalf("expected changed=true; got %+v", res)
	}
}

// TestAggregate_StrictAnyFailure — strict policy short-circuits to
// failed on the first failed child.
func TestAggregate_StrictAnyFailure(t *testing.T) {
	children := []ChildState{
		success(true),
		failure(),
		running(),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Fatalf("expected settled; got %+v", res)
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected failed; got %q", res.ParentState)
	}
	if res.ParentSettlingSignalType != signalpkg.TypePath("terminal/error/aggregate/strict_failed") {
		t.Fatalf("expected strict_failed; got %q", res.ParentSettlingSignalType)
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
	if !res.IsSettled || res.Action != AggregateActionCancelSiblings {
		t.Fatalf("expected cancel-siblings; got %+v", res)
	}
}

// TestAggregate_StrictActiveBlocks — strict policy stays
// non-settled while any child is still running / stale.
func TestAggregate_StrictActiveBlocks(t *testing.T) {
	children := []ChildState{
		success(false),
		running(),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Fatalf("expected non-settled; got %+v", res)
	}
}

// TestAggregate_Threshold — threshold below max_failures → success.
func TestAggregate_ThresholdBelowMax(t *testing.T) {
	children := []ChildState{
		success(true),
		failure(),
		success(false),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFresh {
		t.Fatalf("expected fresh under threshold; got %+v", res)
	}
}

// TestAggregate_ThresholdAtMax — failures ≥ max → failed.
func TestAggregate_ThresholdAtMax(t *testing.T) {
	children := []ChildState{failure(), failure(), success(true)}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected failed at threshold; got %+v", res)
	}
	if res.ParentSettlingSignalType != signalpkg.TypePath("terminal/error/aggregate/threshold_failed") {
		t.Fatalf("expected threshold_failed; got %q", res.ParentSettlingSignalType)
	}
}

// TestAggregate_BestEffort — best_effort accepts any number of failures;
// settles when all children settled.
func TestAggregate_BestEffort(t *testing.T) {
	children := []ChildState{
		failure(),
		failure(),
		success(true),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "best_effort"})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFresh {
		t.Fatalf("expected fresh under best_effort; got %+v", res)
	}
	if !res.ParentChanged {
		t.Fatalf("expected changed=true outcome aggregation; got %+v", res)
	}
}

// TestAggregate_FirstWinner — first success terminates with cancel-non-winners.
func TestAggregate_FirstWinner(t *testing.T) {
	children := []ChildState{
		running(),
		success(true),
		failure(),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "first"})
	if !res.IsSettled || res.Action != AggregateActionCancelNonWinners {
		t.Fatalf("expected first-winner with cancel-non-winners; got %+v", res)
	}
}

// TestAggregate_FirstAllFailed — first policy degrades to failed when
// every child fails before a winner emerges.
func TestAggregate_FirstAllFailed(t *testing.T) {
	children := []ChildState{failure(), failure()}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "first"})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected failed; got %+v", res)
	}
	if res.ParentSettlingSignalType != signalpkg.TypePath("terminal/error/aggregate/first_failed") {
		t.Fatalf("expected first_failed; got %q", res.ParentSettlingSignalType)
	}
}

// TestAggregate_NoChildren — parent stays non-settled when there are
// no children yet.
func TestAggregate_NoChildren(t *testing.T) {
	res := Aggregate(nil, spec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Fatalf("expected non-settled with no children; got %+v", res)
	}
}

// TestAggregate_DefaultPolicyKind — empty kind defaults to strict.
func TestAggregate_DefaultPolicyKind(t *testing.T) {
	children := []ChildState{failure()}
	res := Aggregate(children, spec.AggregationPolicy{})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected strict default → failed; got %+v", res)
	}
}
