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

func TestDeepTree_SubgraphOfFanout(t *testing.T) {
	t.Parallel()
	innerVerdicts := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	outer := runtime.Aggregate(innerVerdicts, tmplspec.AggregationPolicy{Kind: "strict"})
	if !outer.IsSettled {
		t.Fatal("outer fan-out should settle once each sub-graph terminated")
	}
	if outer.ParentState != cascade.NodeStateFresh {
		t.Errorf("outer state: %s (want fresh)", outer.ParentState)
	}
	if outer.ParentSettlingSignalType != signalpkg.TypePath("terminal/success") {
		t.Errorf("outer outcome: %s (want terminal/success)", outer.ParentSettlingSignalType)
	}
	if !outer.ParentChanged {
		t.Errorf("outer changed: false (want true — at least one child changed)")
	}
}

func TestDeepTree_SubgraphOfFanoutOneInnerFails(t *testing.T) {
	t.Parallel()
	innerVerdicts := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	outer := runtime.Aggregate(innerVerdicts, tmplspec.AggregationPolicy{Kind: "strict"})
	if !outer.IsSettled {
		t.Fatal("outer fan-out should settle when any inner reports failed")
	}
	if outer.ParentState != cascade.NodeStateFailed {
		t.Errorf("outer state: %s (want failed)", outer.ParentState)
	}
}

func TestDeepTree_FanoutOfSubgraph(t *testing.T) {
	t.Parallel()
	innerChildren := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	innerVerdict := runtime.Aggregate(innerChildren, tmplspec.AggregationPolicy{Kind: "best_effort"})
	if !innerVerdict.IsSettled {
		t.Fatal("inner fan-out should settle")
	}
	if innerVerdict.ParentState != cascade.NodeStateFresh {
		t.Errorf("inner best_effort verdict: %s (want fresh)", innerVerdict.ParentState)
	}
	outer := runtime.Aggregate([]runtime.ChildState{
		{State: innerVerdict.ParentState, SettlingSignalType: signalpkg.PathPtr(innerVerdict.ParentSettlingSignalType)},
	}, tmplspec.AggregationPolicy{Kind: "strict"})
	if !outer.IsSettled {
		t.Fatal("outer sub-graph should settle on inner verdict")
	}
	if outer.ParentState != cascade.NodeStateFresh {
		t.Errorf("outer state: %s (want fresh)", outer.ParentState)
	}
}
