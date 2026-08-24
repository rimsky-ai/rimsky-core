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

func TestFanoutAggregation_EmptyChildrenStaysRunning(t *testing.T) {
	t.Parallel()
	res := runtime.Aggregate(nil, tmplspec.AggregationPolicy{Kind: "strict"})
	if res.IsSettled {
		t.Errorf("empty children: parent must stay non-terminal; got %s", res.ParentState)
	}
}

func TestFanoutAggregation_UnknownPolicyFallsBackToStrict(t *testing.T) {
	t.Parallel()
	mixed := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(mixed, tmplspec.AggregationPolicy{Kind: "some-future-policy"})
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("unknown policy fallback: parent state %s (want strict→failed)", res.ParentState)
	}
}
