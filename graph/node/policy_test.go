// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/graph/shared"
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
	require.Equal(t, 100, r1.DelayMs) // linear, attemptIndex 0 → base*1
	require.Equal(t, 1, r1.NewState.RetryCounter)
	require.Equal(t, 0, r1.NewState.ActionIndex)
	require.Equal(t, "boom", r1.NewState.CurrentErrorClass)

	r2 := Evaluate(policy, r1.NewState, "boom", nil)
	require.Equal(t, "retry", r2.Kind)
	require.Equal(t, 200, r2.DelayMs) // linear, attemptIndex 1 → base*2
	require.Equal(t, 2, r2.NewState.RetryCounter)
}

// TestRetryExhaustsAdvancesActionIndex exercises the chain advancing
// past a retry-exhausted entry into the next entry. Pre-2026-05-23 the
// next entry was `invalidate` (now retired); we use `give_up` instead,
// which is the canonical chain terminator.
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
	require.Equal(t, 100, r2.DelayMs) // first-attempt delay, not boom continuation
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
	r1 := Evaluate(policy, state, "boom", nil) // retry fires
	state = r1.NewState
	r2 := Evaluate(policy, state, "boom", nil) // retry exhausted, no next action
	require.Equal(t, "give_up", r2.Kind)
	require.Equal(t, "policy_exhausted", r2.Reason)
}

// TestDiscardClaimsThenRetryPropagatesKind covers the rename of the
// pre-2026-05-23 `discard_then_retry` action to
// `discard_claims_then_retry` (the new name makes clear the verb fires
// on the claim handles, not the node row).
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

// TestRetryFlavorsExhaustAdvanceActionIndex covers the two retry
// flavors (retry, discard_claims_then_retry) advancing past an
// exhausted chain entry into the next entry.
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

// TestPassSettlesFreshAndAdvancesChain covers the new `pass` action.
// Pass settles the run as fresh (the runtime-side `Resolution.Color`
// translates Kind=pass → ColorFresh) and advances the chain so a
// subsequent same-class error doesn't `pass` again.
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
		return 0.5 // → 0.5 + 0.5 = 1.0 multiplier → delay unchanged
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
