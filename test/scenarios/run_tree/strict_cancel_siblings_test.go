// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N1 scenario — strict_cancel_siblings.
//
// `strict.cancel_siblings: true` requests AggregateActionCancelSiblings
// on any failure; the supervisor's terminal handler then walks running
// siblings and transitions them to failed in the same transaction.
package runtree

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestStrictCancelSiblings_ActionFiresOnFailure(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateRunning},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict", CancelSiblings: true})
	if !res.IsSettled {
		t.Fatal("strict any-failed: parent should settle terminal")
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("parent state: %s (want failed)", res.ParentState)
	}
	if res.Action != runtime.AggregateActionCancelSiblings {
		t.Errorf("expected AggregateActionCancelSiblings, got %v", res.Action)
	}
}

func TestStrictCancelSiblings_NotFiredWhenFlagOff(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateRunning},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.Action != runtime.AggregateActionNone {
		t.Errorf("strict without cancel_siblings flag: action %v (want none)", res.Action)
	}
}

func TestStrictCancelSiblings_NotFiredOnAllSuccess(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict", CancelSiblings: true})
	if res.Action != runtime.AggregateActionNone {
		t.Errorf("strict all-success: action %v (want none — nothing to cancel)", res.Action)
	}
}
