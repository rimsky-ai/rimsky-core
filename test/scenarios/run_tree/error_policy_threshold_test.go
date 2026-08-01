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

func TestErrorPolicyThreshold_BelowMaxSucceeds(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsSettled {
		t.Fatal("threshold should settle when all children terminal")
	}
	if res.ParentState != cascade.NodeStateFresh {
		t.Errorf("below-max-failures: parent state %s (want fresh)", res.ParentState)
	}
}

func TestErrorPolicyThreshold_AtMaxFails(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 2})
	if !res.IsSettled {
		t.Fatal("threshold should settle when all children terminal")
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("at-max-failures: parent state %s (want failed)", res.ParentState)
	}
}

func TestErrorPolicyThreshold_RunningChildStillBlocks(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateRunning},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 5})
	if res.IsSettled {
		t.Errorf("threshold must wait for all children to settle; got terminal=%s", res.ParentState)
	}
}
