// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N1 scenario — error_policy_threshold.
//
// `threshold` aggregation: parent fails when failure count reaches
// max_failures; otherwise succeeds once all children settle.
package runtree

import (
	"testing"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	signalpkg "github.com/fallguyconsulting/rimsky/foundation/signal"
	tmplspec "github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/runtime"
)

func TestErrorPolicyThreshold_BelowMaxSucceeds(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
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
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.TypePath("terminal/success"), Changed: true},
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
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.TypePath("terminal/error/test_failure")},
		{State: cascade.NodeStateRunning},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "threshold", MaxFailures: 5})
	if res.IsSettled {
		t.Errorf("threshold must wait for all children to settle; got terminal=%s", res.ParentState)
	}
}
