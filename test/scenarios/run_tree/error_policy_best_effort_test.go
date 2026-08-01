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

func TestErrorPolicyBestEffort_FailuresDontBlock(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "best_effort"})
	if !res.IsSettled {
		t.Fatal("best_effort should settle when all children terminal")
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Errorf("best_effort with mixed outcomes: parent state %s (want fresh)", res.ParentState)
	}
}

func TestErrorPolicyBestEffort_AllFailedStillSucceeds(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "best_effort"})
	if !res.IsSettled {
		t.Fatal("best_effort should always settle when all children terminal")
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Errorf("best_effort all-failed: parent state %s (want fresh)", res.ParentState)
	}
}

func TestErrorPolicyBestEffort_RunningChildStillBlocks(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateRunning},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "best_effort"})
	if res.IsSettled {
		t.Errorf("best_effort must wait for all children to settle; got terminal=%s", res.ParentState)
	}
}
