// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Fatal("strict any-failed: parent should settle terminal")
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("parent state: %s (want failed)", res.ParentState)
	}
	if res.Action != runtime.AggregateActionCancelSiblings {
		t.Errorf("strict must always cancel in-flight siblings on failure; got %v", res.Action)
	}
}

func TestStrictCancelSiblings_NotFiredOnAllSuccess(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.Action != runtime.AggregateActionNone {
		t.Errorf("strict all-success: action %v (want none — nothing to cancel)", res.Action)
	}
}
