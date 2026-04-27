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

// PolicyAction is one entry in a node's per-error-class repair chain.
//
// Action vocabulary (spec §12.6 + §13.6):
//   - "retry"              — generic retry; the runner picks the release
//     mode from the spec's `resumable` flag (back-compat shape).
//   - "discard_then_retry" — explicitly request `ReleaseLock(give_up)`
//     before re-enqueue. Sidecar (post-v1) discards in-flight writes;
//     direct mode is effectively keep-then-retry per §6.1.
//   - "resume_then_retry"  — explicitly request
//     `ReleaseLock(preserve_for_resume)` before re-enqueue. Requires at
//     least one acq lock spec to declare `resumable: true`; the runner
//     falls back to give_up otherwise.
//   - "invalidate"         — return targets; lock release goes through
//     give_up.
//   - "give_up"            — terminal failure; lock release goes through
//     give_up.
type PolicyAction struct {
	Action         string
	Count          int
	Backoff        shared.BackoffKind
	Jitter         shared.JitterKind
	BaseDelayMs    int
	MaxDelayMs     int
	Targets        []string
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

// ResolvedAction is the outcome of one Evaluate call. Kind carries the
// runtime intent the runner branches on:
//
//   - "retry"              — generic retry (release mode picked by runner).
//   - "discard_then_retry" — retry with explicit give_up release.
//   - "resume_then_retry"  — retry with explicit preserve_for_resume release.
//   - "invalidate"         — targets returned in Targets.
//   - "give_up"            — terminal.
type ResolvedAction struct {
	Kind     string
	DelayMs  int
	Targets  []string
	Reason   string
	NewState EvaluatorState
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
	case "retry", "discard_then_retry", "resume_then_retry":
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
	case "invalidate":
		return ResolvedAction{
			Kind:    "invalidate",
			Targets: action.Targets,
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
