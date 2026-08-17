// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func success(changed bool) ChildState {
	return ChildState{
		State:              cascade.NodeStateFresh,
		SettlingSignalType: signalpkg.PathPtr("terminal/success"),
		Changed:            changed,
	}
}

func failure() ChildState {
	return ChildState{
		State:              cascade.NodeStateFailed,
		SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure"),
	}
}

func running() ChildState {
	return ChildState{State: cascade.NodeStateRunning}
}

func parked() ChildState {
	return ChildState{State: cascade.NodeStateParked}
}

func TestChildState_IsSuccess_NilSettlingSignalTypeIsSuccess(t *testing.T) {
	c := ChildState{State: cascade.NodeStateFresh, SettlingSignalType: nil}
	if !c.IsSuccess() {
		t.Fatal("a Fresh child with a nil SettlingSignalType must count as success — " +
			"nil means no signal was recorded, not that the child failed")
	}
}

func TestAggregate_FirstWinner_NilSettlingSignalTypeDefaultsToTerminalSuccess(t *testing.T) {
	children := []ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: nil, Changed: true},
		running(),
	}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "first"})
	if !res.IsSettled || res.Action != AggregateActionCancelNonWinners {
		t.Fatalf("expected a nil-signal Fresh child to win under first policy; got %+v", res)
	}
	if res.ParentSettlingSignalType != signalpkg.TypePath("terminal/success") {
		t.Fatalf("a winning child with nil SettlingSignalType must fall back to terminal/success; got %q",
			res.ParentSettlingSignalType)
	}
	if !res.ParentChanged {
		t.Fatalf("expected the winner's Changed flag to propagate; got %+v", res)
	}
}

func TestChildState_IsSettled_ParkedIsNotSettled(t *testing.T) {
	if parked().IsSettled() {
		t.Fatal("parked must not count as settled — it is an in-flight state per concept:node-run's " +
			"{pending, stale, running, held, parked} set")
	}
}

func TestAggregate_Strict_ParkedChildKeepsParentWaiting(t *testing.T) {
	children := []ChildState{success(true), parked()}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Fatalf("a parked sibling must hold strict aggregation open; got %+v", res)
	}
}

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
	if res.Action != AggregateActionNone {
		t.Fatalf("a strict fan-out whose children all succeeded must cancel nothing; got %v", res.Action)
	}
}

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
	if res.Action != AggregateActionCancelSiblings {
		t.Fatalf("expected strict to always cancel in-flight siblings on failure; got %v", res.Action)
	}
}

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

func TestAggregate_ThresholdAtFullCount_KeepsRunning(t *testing.T) {
	children := []ChildState{failure(), running(), success(true)}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "threshold", MaxFailures: len(children)})
	if res.IsSettled {
		t.Fatalf("threshold at full count must not settle while a sibling is still in flight; got %+v", res)
	}
	if res.Action != AggregateActionNone {
		t.Fatalf("threshold never cancels siblings; got %v", res.Action)
	}
}

func TestAggregate_ThresholdAtFullCount_PartialSuccessSettlesFresh(t *testing.T) {
	children := []ChildState{failure(), success(true), success(true)}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "threshold", MaxFailures: len(children)})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFresh {
		t.Fatalf("threshold at full count must accept a partial outcome once every child is terminal; got %+v", res)
	}
	if res.Action != AggregateActionNone {
		t.Fatalf("threshold never cancels siblings; got %v", res.Action)
	}
}

func TestAggregate_ThresholdAtFullCount_AllFailedSettlesFailed(t *testing.T) {
	children := []ChildState{failure(), failure(), failure()}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "threshold", MaxFailures: len(children)})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("threshold at full count must fail once every child has failed; got %+v", res)
	}
	if res.ParentSettlingSignalType != signalpkg.TypePath("terminal/error/aggregate/threshold_failed") {
		t.Fatalf("expected threshold_failed; got %q", res.ParentSettlingSignalType)
	}
}

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

func TestAggregate_NoChildren(t *testing.T) {
	res := Aggregate(nil, spec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Fatalf("expected non-settled with no children; got %+v", res)
	}
}

func TestAggregate_DefaultPolicyKind(t *testing.T) {
	children := []ChildState{failure()}
	res := Aggregate(children, spec.AggregationPolicy{})
	if !res.IsSettled || res.ParentState != cascade.NodeStateFailed {
		t.Fatalf("expected strict default → failed; got %+v", res)
	}
}

func TestAggregate_UnknownKindFallsBackToStrict(t *testing.T) {
	children := []ChildState{failure(), success(true)}
	res := Aggregate(children, spec.AggregationPolicy{Kind: "bogus-unknown"})
	if !res.IsSettled {
		t.Fatalf("unknown kind should default-fall through to strict (which settles on failure)")
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("unknown kind fallback (strict) should fail on any-failed child; got state %s", res.ParentState)
	}
}

func TestAggregate_AllRecognizedKinds_AllSuccessSettlesFresh(t *testing.T) {
	children := []ChildState{success(true), success(true)}
	for _, kind := range []spec.AggregationKind{
		spec.AggregationKindStrict,
		spec.AggregationKindThreshold,
		spec.AggregationKindBestEffort,
		spec.AggregationKindFirst,
	} {
		t.Run(string(kind), func(t *testing.T) {
			policy := spec.AggregationPolicy{Kind: kind}
			if kind == spec.AggregationKindThreshold {
				policy.MaxFailures = 1
			}
			res := Aggregate(children, policy)
			if !res.IsSettled {
				t.Errorf("policy %s on all-success children should settle terminal", kind)
			}
			if res.ParentState != cascade.NodeStateFresh {
				t.Errorf("policy %s parent state: %s (want fresh)", kind, res.ParentState)
			}
		})
	}
}
