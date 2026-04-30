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
// Action vocabulary (carried forward through the 2026-04-30 stores
// cleanup; the rimsky-side substrate verb is the success/failure
// binary — success → Commit, failure → Abandon — and the substrate
// decides what those mean for its own state per its own configuration):
//   - "retry"              — generic retry; the runner releases the
//     claim by firing Abandon on the substrate before re-enqueue.
//   - "discard_then_retry" — explicitly request `Abandon` (staged
//     stores) or release-by-Abandon (pick-policy substrates) before
//     re-enqueue. The v3 standard filesystem store is `direct`-only;
//     for direct stores Abandon is degenerate (writes cannot be
//     undone), so discard_then_retry is effectively keep-then-retry
//     on those stores.
//   - "resume_then_retry"  — historical action vocabulary preserved
//     for backwards-compatible policy declarations. Behaviorally an
//     alias for `discard_then_retry`: the runner releases each claim
//     by firing `Abandon` on the substrate before re-enqueue. Explicit
//     Release-routing for read-side state is not in scope for the
//     2026-04-30 stores cleanup; if a future cycle reintroduces it,
//     update both this comment and `applyResolvedAction` together.
//   - "invalidate"         — return targets; per-claim release fires
//     Abandon on the substrate.
//   - "give_up"            — terminal failure; per-claim release
//     fires Abandon on the substrate.
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
//   - "retry"              — generic retry; runner fires Abandon on
//     the substrate before re-enqueue.
//   - "discard_then_retry" — retry with explicit Abandon (or Release
//     for substrates with read-side state at Open).
//   - "resume_then_retry"  — alias for `discard_then_retry`; the
//     runner fires `Abandon` on each claim before re-enqueue. Kept as
//     a distinct kind so policy authors can express intent in
//     declarations; the runtime routing is identical to retry today.
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
