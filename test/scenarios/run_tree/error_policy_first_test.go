// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N1 scenario — error_policy_first.
//
// `first` aggregation: first success → parent success +
// AggregateActionCancelNonWinners; all failed → failed.
package runtree

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestErrorPolicyFirst_OneWinnerCancelsOthers(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateRunning},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateStale},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "first"})
	if !res.IsSettled {
		t.Fatal("first winner: parent should settle terminal")
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Errorf("first winner parent state: %s (want fresh)", res.ParentState)
	}
	if res.Action != runtime.AggregateActionCancelNonWinners {
		t.Errorf("first winner action: %v (want CancelNonWinners)", res.Action)
	}
}

func TestErrorPolicyFirst_AllFailedFails(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "first"})
	if !res.IsSettled {
		t.Fatal("first all-failed: parent should settle")
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("first all-failed parent state: %s (want failed)", res.ParentState)
	}
}

func TestErrorPolicyFirst_OneRunningHoldsTerminalUnlessWinner(t *testing.T) {
	t.Parallel()
	// One failed, one running, none succeeded: parent stays non-terminal.
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
		{State: cascade.NodeStateRunning},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "first"})
	if res.IsSettled {
		t.Errorf("first with no winner yet: parent must stay non-terminal; got %s", res.ParentState)
	}
}
