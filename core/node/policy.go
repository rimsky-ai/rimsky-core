package node

import (
	"math/rand"

	"github.com/fallguy/rimsky/core/shared"
)

// ErrorTypePolicy and PolicyAction describe the per-error-class repair
// chains declared in a node's error_types template block.
type ErrorTypePolicy struct {
	Policy []PolicyAction
}

type PolicyAction struct {
	Action         string // "retry" | "invalidate" | "give_up"
	Count          int
	Backoff        shared.BackoffKind
	Jitter         shared.JitterKind
	BaseDelayMs    int
	MaxDelayMs     int
	Targets        []string
	RestoreVersion string // "previous" | "" | version id
	ReasonTemplate string
}

// EvaluatorState is the persisted per-node, per-error-class policy chain
// position. Stored on the rimsky_nodes row as (current_error_class,
// retry_counter, action_index).
type EvaluatorState struct {
	ActionIndex       int
	RetryCounter      int
	CurrentErrorClass string
}

type ResolvedAction struct {
	Kind           string // "retry" | "invalidate" | "give_up"
	DelayMs        int
	Targets        []string
	RestoreVersion string
	Reason         string
	NewState       EvaluatorState
}

// Evaluate advances the policy chain by one step for a given error
// occurrence. See spec §4.2 and §7.3.
//   - policy == nil (unknown error class) → give_up("unknown_error_class")
//   - different error class → reset counters
//   - retry: if counter < count, increment + schedule backoff; else advance action_index and recurse
//   - invalidate: return targets; action_index advances immediately so same-class recurrence moves on
//   - give_up: terminal
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
	case "retry":
		if state.RetryCounter < action.Count {
			newCounter := state.RetryCounter + 1
			delay := ComputeDelay(BackoffConfig{
				Kind:        action.Backoff,
				BaseDelayMs: action.BaseDelayMs,
				Jitter:      action.Jitter,
				MaxDelayMs:  action.MaxDelayMs,
			}, newCounter-1, rng)
			return ResolvedAction{
				Kind:    "retry",
				DelayMs: delay,
				NewState: EvaluatorState{
					ActionIndex: state.ActionIndex, RetryCounter: newCounter, CurrentErrorClass: errorClass,
				},
			}
		}
		return step(chain, EvaluatorState{
			ActionIndex: state.ActionIndex + 1, RetryCounter: 0, CurrentErrorClass: errorClass,
		}, errorClass, rng)
	case "invalidate":
		restore := action.RestoreVersion
		return ResolvedAction{
			Kind:           "invalidate",
			Targets:        action.Targets,
			RestoreVersion: restore,
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
