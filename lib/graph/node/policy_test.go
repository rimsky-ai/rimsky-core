// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func linearBackoff(baseMs int) BackoffConfig {
	return BackoffConfig{Kind: spec.BackoffLinear, BaseDelayMs: baseMs, Jitter: spec.JitterNone}
}

func TestEvaluate_UnknownErrorClassGivesUp(t *testing.T) {
	r := Evaluate(nil, EvaluatorState{}, 10, BackoffConfig{}, nil)
	require.Equal(t, spec.ActionGiveUp, r.Kind)
	require.Equal(t, "unknown_error_class", r.Reason)
	require.Equal(t, 0, r.NewState.RetryCounter)
}

func TestEvaluate_RetryIncrementsCounter(t *testing.T) {
	policy := &ErrorTypePolicy{Action: spec.ActionRetry}
	r1 := Evaluate(policy, EvaluatorState{}, 10, linearBackoff(100), nil)
	require.Equal(t, spec.ActionRetry, r1.Kind)
	require.Equal(t, 100, r1.DelayMs)
	require.Equal(t, 1, r1.NewState.RetryCounter)

	r2 := Evaluate(policy, r1.NewState, 10, linearBackoff(100), nil)
	require.Equal(t, spec.ActionRetry, r2.Kind)
	require.Equal(t, 200, r2.DelayMs)
	require.Equal(t, 2, r2.NewState.RetryCounter)
}

func TestEvaluate_RetryAtCapForcesGiveUp(t *testing.T) {
	policy := &ErrorTypePolicy{Action: spec.ActionRetry}
	r := Evaluate(policy, EvaluatorState{RetryCounter: 3}, 3, linearBackoff(100), nil)
	require.Equal(t, spec.ActionGiveUp, r.Kind)
	require.Equal(t, "max_retries_exhausted", r.Reason)
	require.Equal(t, 3, r.NewState.RetryCounter)
}

func TestEvaluate_RetryUnboundedWhenMaxRetriesZero(t *testing.T) {
	policy := &ErrorTypePolicy{Action: spec.ActionRetry}
	r := Evaluate(policy, EvaluatorState{RetryCounter: 9999}, 0, linearBackoff(50), nil)
	require.Equal(t, spec.ActionRetry, r.Kind, "MaxRetries=0 disables the cap")
	require.Equal(t, 10000, r.NewState.RetryCounter)
}

func TestEvaluate_PassReturnsPass(t *testing.T) {
	policy := &ErrorTypePolicy{Action: spec.ActionPass}
	r := Evaluate(policy, EvaluatorState{RetryCounter: 5}, 10, BackoffConfig{}, nil)
	require.Equal(t, spec.ActionPass, r.Kind)
	require.Equal(t, 5, r.NewState.RetryCounter)
}

func TestEvaluate_GiveUpReturnsGiveUpWithReason(t *testing.T) {
	policy := &ErrorTypePolicy{Action: spec.ActionGiveUp, ReasonTemplate: "fatal_oops"}
	r := Evaluate(policy, EvaluatorState{}, 10, BackoffConfig{}, nil)
	require.Equal(t, spec.ActionGiveUp, r.Kind)
	require.Equal(t, "fatal_oops", r.Reason)
}

func TestEvaluate_ReleaseAndRequeue(t *testing.T) {
	policy := &ErrorTypePolicy{Action: spec.ActionReleaseAndRequeue}
	r := Evaluate(policy, EvaluatorState{RetryCounter: 2}, 10, BackoffConfig{}, nil)
	require.Equal(t, spec.ActionReleaseAndRequeue, r.Kind)
	require.Equal(t, 2, r.NewState.RetryCounter)
}

func TestEvaluate_UnknownActionFallsToGiveUp(t *testing.T) {
	policy := &ErrorTypePolicy{Action: "make_up_action"}
	r := Evaluate(policy, EvaluatorState{}, 10, BackoffConfig{}, nil)
	require.Equal(t, spec.ActionGiveUp, r.Kind)
	require.Equal(t, "unknown_action_type", r.Reason)
}

func TestEvaluate_BackoffJitterConsumesRng(t *testing.T) {
	policy := &ErrorTypePolicy{Action: spec.ActionRetry}
	backoff := BackoffConfig{Kind: spec.BackoffLinear, BaseDelayMs: 100, Jitter: spec.JitterPlusMinus}
	calls := 0
	rng := func() float64 {
		calls++
		return 0.5
	}
	r := Evaluate(policy, EvaluatorState{}, 10, backoff, rng)
	require.Equal(t, spec.ActionRetry, r.Kind)
	require.Equal(t, 1, calls)
}
