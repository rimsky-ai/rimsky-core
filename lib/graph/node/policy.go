// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"math/rand"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type (
	ErrorTypePolicy = spec.ErrorTypePolicy
	PolicyAction    = spec.PolicyAction
	EvaluatorState  = spec.EvaluatorState
	ResolvedAction  = spec.ResolvedAction
)

func Evaluate(policy *ErrorTypePolicy, state EvaluatorState, errorClass string, rng func() float64) ResolvedAction {
	if rng == nil {
		rng = rand.Float64
	}
	if policy == nil {
		return ResolvedAction{
			Kind:   "give_up",
			Reason: "unknown_error_class",
			NewState: EvaluatorState{
				ActionIndex: 0, RetryCounter: 0, CurrentErrorClass: errorClass,
			},
		}
	}
	working := state
	if working.CurrentErrorClass != errorClass {
		working = EvaluatorState{ActionIndex: 0, RetryCounter: 0, CurrentErrorClass: errorClass}
	}
	return step(policy.Policy, working, errorClass, rng)
}

func step(chain []PolicyAction, state EvaluatorState, errorClass string, rng func() float64) ResolvedAction {
	if state.ActionIndex >= len(chain) {
		return ResolvedAction{
			Kind:   "give_up",
			Reason: "policy_exhausted",
			NewState: EvaluatorState{
				ActionIndex: state.ActionIndex, RetryCounter: 0, CurrentErrorClass: errorClass,
			},
		}
	}
	action := chain[state.ActionIndex]
	switch action.Action {
	case "retry", "discard_claims_then_retry":
		if state.RetryCounter < action.Count {
			newCounter := state.RetryCounter + 1
			delay := ComputeDelay(BackoffConfig{
				Kind:        action.Backoff,
				BaseDelayMs: action.BaseDelayMs,
				Jitter:      action.Jitter,
				MaxDelayMs:  action.MaxDelayMs,
			}, newCounter-1, rng)
			return ResolvedAction{
				Kind:    action.Action,
				DelayMs: delay,
				NewState: EvaluatorState{
					ActionIndex: state.ActionIndex, RetryCounter: newCounter, CurrentErrorClass: errorClass,
				},
			}
		}
		return step(chain, EvaluatorState{
			ActionIndex: state.ActionIndex + 1, RetryCounter: 0, CurrentErrorClass: errorClass,
		}, errorClass, rng)
	case "pass":
		return ResolvedAction{
			Kind: "pass",
			NewState: EvaluatorState{
				ActionIndex: state.ActionIndex + 1, RetryCounter: 0, CurrentErrorClass: errorClass,
			},
		}
	case "give_up":
		reason := action.ReasonTemplate
		if reason == "" {
			reason = "give_up"
		}
		return ResolvedAction{
			Kind:   "give_up",
			Reason: reason,
			NewState: EvaluatorState{
				ActionIndex: state.ActionIndex, RetryCounter: 0, CurrentErrorClass: errorClass,
			},
		}
	default:
		return ResolvedAction{
			Kind:     "give_up",
			Reason:   "unknown_action_type",
			NewState: state,
		}
	}
}
