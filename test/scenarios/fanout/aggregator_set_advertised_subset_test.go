// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package fanout

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestAggregatorSet_RecognizedKindsAccepted(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	for _, kind := range []tmplspec.AggregationKind{
		tmplspec.AggregationKindStrict,
		tmplspec.AggregationKindThreshold,
		tmplspec.AggregationKindBestEffort,
		tmplspec.AggregationKindFirst,
	} {
		t.Run(string(kind), func(t *testing.T) {
			policy := tmplspec.AggregationPolicy{Kind: kind}
			if kind == tmplspec.AggregationKindThreshold {
				policy.MaxFailures = 1
			}
			res := runtime.Aggregate(children, policy)
			if !res.IsSettled {
				t.Errorf("policy %s on all-success children should settle terminal", kind)
			}
			if res.ParentState != cascade.NodeStateFresh {
				t.Errorf("policy %s parent state: %s (want fresh)", kind, res.ParentState)
			}
		})
	}
}

func TestAggregatorSet_UnknownKindFallsBackToStrict(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: "bogus-unknown"})
	if !res.IsSettled {
		t.Fatalf("unknown kind should default-fall through to strict (which settles on failure)")
	}
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("unknown kind fallback (strict) should fail on any-failed child; got state %s", res.ParentState)
	}
}

func TestAggregatorSet_EmptyKindDefaultsToStrict(t *testing.T) {
	t.Parallel()
	children := []runtime.ChildState{
		{State: cascade.NodeStateFailed, SettlingSignalType: signalpkg.PathPtr("terminal/error/test_failure")},
		{State: cascade.NodeStateFresh, SettlingSignalType: signalpkg.PathPtr("terminal/success"), Changed: true},
	}
	res := runtime.Aggregate(children, tmplspec.AggregationPolicy{Kind: ""})
	if res.ParentState != cascade.NodeStateFailed {
		t.Errorf("empty kind defaults to strict; got state %s", res.ParentState)
	}
}
