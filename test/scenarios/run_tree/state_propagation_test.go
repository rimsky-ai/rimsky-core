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

func TestStatePropagation_NonTerminalChildHoldsParent(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateRunning},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Errorf("parent must stay non-terminal while a child is running; got terminal=%s", res.ParentState)
	}
}

func TestStatePropagation_AllStaleStillNonTerminal(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateStale},
		{State: cascade.NodeStateStale},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Errorf("all-stale children: parent must stay non-terminal; got %s", res.ParentState)
	}
}

func TestStatePropagation_ParkedChildAggregatesAsTerminal(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateParked},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Errorf("parked child should be treated as terminal by Aggregate; got non-terminal")
	}
}

func TestStatePropagation_FreshUnchangedAggregatesToParent(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Fatal("all-fresh children should settle the parent")
	}
	if res.ParentChanged {
		t.Errorf("all-unchanged children: parent outcome %s (want fresh_unchanged)", res.ParentSettlingSignalType)
	}
}

func TestStatePropagation_FreshChangedDominatesUnchanged(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "strict"})
	if !res.IsSettled {
		t.Fatal("all-fresh children should settle the parent")
	}
	if !res.ParentChanged {
		t.Errorf("any-changed child: parent outcome %s (want fresh_changed)", res.ParentSettlingSignalType)
	}
}
