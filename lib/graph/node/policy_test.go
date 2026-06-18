// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/shared"
)

func initialState() EvaluatorState {
	return EvaluatorState{ActionIndex: 0, RetryCounter: 0, CurrentErrorClass: ""}
}

func TestUnknownErrorClassGivesUp(t *testing.T) {
	r := Evaluate(nil, initialState(), "mystery", nil)
	require.Equal(t, "give_up", r.Kind)
	require.Equal(t, "unknown_error_class", r.Reason)
	require.Equal(t, "mystery", r.NewState.CurrentErrorClass)
}

func TestRetryIncrementsCounter(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{{
			Action:      "retry",
			Count:       3,
			Backoff:     shared.BackoffLinear,
			BaseDelayMs: 100,
			Jitter:      shared.JitterNone,
		}},
	}
	r1 := Evaluate(policy, initialState(), "boom", nil)
	require.Equal(t, "retry", r1.Kind)
	require.Equal(t, 100, r1.DelayMs)
	require.Equal(t, 1, r1.NewState.RetryCounter)
	require.Equal(t, 0, r1.NewState.ActionIndex)
	require.Equal(t, "boom", r1.NewState.CurrentErrorClass)

	r2 := Evaluate(policy, r1.NewState, "boom", nil)
	require.Equal(t, "retry", r2.Kind)
	require.Equal(t, 200, r2.DelayMs)
	require.Equal(t, 2, r2.NewState.RetryCounter)
}

func TestRetryExhaustsAdvancesActionIndex(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{
			{
				Action:      "retry",
				Count:       1,
				Backoff:     shared.BackoffLinear,
				BaseDelayMs: 100,
				Jitter:      shared.JitterNone,
			},
			{Action: "give_up", ReasonTemplate: "after_retry"},
		},
	}
	state := initialState()
	r1 := Evaluate(policy, state, "boom", nil)
	require.Equal(t, "retry", r1.Kind)
	state = r1.NewState

	r2 := Evaluate(policy, state, "boom", nil)
	require.Equal(t, "give_up", r2.Kind)
	require.Equal(t, "after_retry", r2.Reason)
}

func TestGiveUpTerminal(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{{Action: "give_up", ReasonTemplate: "fatal_oops"}},
	}
	r := Evaluate(policy, initialState(), "boom", nil)
	require.Equal(t, "give_up", r.Kind)
	require.Equal(t, "fatal_oops", r.Reason)
}

func TestDifferentErrorClassResetsCounters(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{{
			Action:      "retry",
			Count:       5,
			Backoff:     shared.BackoffLinear,
			BaseDelayMs: 100,
			Jitter:      shared.JitterNone,
		}},
	}
	r1 := Evaluate(policy, initialState(), "boom", nil)
	require.Equal(t, "retry", r1.Kind)
	require.Equal(t, 1, r1.NewState.RetryCounter)
	require.Equal(t, "boom", r1.NewState.CurrentErrorClass)

	r2 := Evaluate(policy, r1.NewState, "other", nil)
	require.Equal(t, "retry", r2.Kind)
	require.Equal(t, 1, r2.NewState.RetryCounter)
	require.Equal(t, "other", r2.NewState.CurrentErrorClass)
	require.Equal(t, 100, r2.DelayMs)
}

func TestPolicyExhaustedFallsThroughToGiveUp(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{{
			Action:      "retry",
			Count:       1,
			Backoff:     shared.BackoffLinear,
			BaseDelayMs: 100,
			Jitter:      shared.JitterNone,
		}},
	}
	state := initialState()
	r1 := Evaluate(policy, state, "boom", nil)
	state = r1.NewState
	r2 := Evaluate(policy, state, "boom", nil)
	require.Equal(t, "give_up", r2.Kind)
	require.Equal(t, "policy_exhausted", r2.Reason)
}

func TestDiscardClaimsThenRetryPropagatesKind(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{{
			Action:      "discard_claims_then_retry",
			Count:       2,
			Backoff:     shared.BackoffLinear,
			BaseDelayMs: 50,
			Jitter:      shared.JitterNone,
		}},
	}
	r := Evaluate(policy, initialState(), "boom", nil)
	require.Equal(t, "discard_claims_then_retry", r.Kind)
	require.Equal(t, 50, r.DelayMs)
	require.Equal(t, 1, r.NewState.RetryCounter)
}

func TestRetryFlavorsExhaustAdvanceActionIndex(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{
			{
				Action:      "discard_claims_then_retry",
				Count:       1,
				Backoff:     shared.BackoffLinear,
				BaseDelayMs: 100,
				Jitter:      shared.JitterNone,
			},
			{Action: "give_up", ReasonTemplate: "fatal"},
		},
	}
	r1 := Evaluate(policy, initialState(), "boom", nil)
	require.Equal(t, "discard_claims_then_retry", r1.Kind)

	r2 := Evaluate(policy, r1.NewState, "boom", nil)
	require.Equal(t, "give_up", r2.Kind)
	require.Equal(t, "fatal", r2.Reason)
}

func TestPassSettlesFreshAndAdvancesChain(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{
			{Action: "pass"},
			{Action: "give_up", ReasonTemplate: "second_time_unlucky"},
		},
	}
	r1 := Evaluate(policy, initialState(), "boom", nil)
	require.Equal(t, "pass", r1.Kind)
	require.Equal(t, 1, r1.NewState.ActionIndex)
	require.Equal(t, 0, r1.NewState.RetryCounter)
	require.Equal(t, "boom", r1.NewState.CurrentErrorClass)

	r2 := Evaluate(policy, r1.NewState, "boom", nil)
	require.Equal(t, "give_up", r2.Kind)
	require.Equal(t, "second_time_unlucky", r2.Reason)
}

func TestBackoffJitterConsumesRng(t *testing.T) {
	policy := &ErrorTypePolicy{
		Policy: []PolicyAction{{
			Action:      "retry",
			Count:       3,
			Backoff:     shared.BackoffLinear,
			BaseDelayMs: 100,
			Jitter:      shared.JitterPlusMinus,
		}},
	}
	calls := 0
	rng := func() float64 {
		calls++
		return 0.5
	}
	r1 := Evaluate(policy, initialState(), "boom", rng)
	require.Equal(t, "retry", r1.Kind)
	require.Equal(t, 1, calls)
	require.Equal(t, 100, r1.DelayMs)

	r2 := Evaluate(policy, r1.NewState, "boom", rng)
	require.Equal(t, "retry", r2.Kind)
	require.Equal(t, 2, calls)
	require.Equal(t, 200, r2.DelayMs)
}
