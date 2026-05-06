// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cascade

import (
	"fmt"

	"github.com/fallguy/rimsky/modeling/shared"
)

// TransitionReason identifies WHY a state transition was requested. Preserved
// from TS v1's discriminated union plus the Go port's `pure_cascade` addition.
type TransitionReason struct {
	Kind string
}

var (
	ReasonInvalidateReceived = TransitionReason{Kind: "invalidate_received"}
	ReasonDispatchClaimed    = TransitionReason{Kind: "dispatch_claimed"}
	// ReasonWorkCompleted is the legacy name for the running → fresh
	// transition triggered by the supervisor's terminal-Complete handler.
	// Deprecated for new code paths in favor of ReasonHandlerComplete
	// (see the lifecycle-handler design at
	// .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md).
	// Retained as an alias for one cycle to ease the doc / annotation
	// migration; existing callers may still emit this kind.
	ReasonWorkCompleted      = TransitionReason{Kind: "work_completed"}
	ReasonPolicyRetry        = TransitionReason{Kind: "policy_retry"}
	ReasonPolicyInvalidate   = TransitionReason{Kind: "policy_invalidate"}
	ReasonPolicyGiveUp       = TransitionReason{Kind: "policy_give_up"}
	ReasonOperatorReset      = TransitionReason{Kind: "operator_reset"}
	ReasonOperatorInvalidate = TransitionReason{Kind: "operator_invalidate"}
	ReasonHeartbeatLost      = TransitionReason{Kind: "heartbeat_lost"}
	// ReasonInfraReenqueue is used when ApplyTerminalOutcome observes an
	// infra-level failure (stream_error, executor_dial_failed,
	// stream_closed_without_terminal, etc.) and re-enqueues the node for
	// another attempt without bumping the retry counter. Semantically it's
	// identical to `running → stale` but distinguishes an explicit infra
	// re-enqueue from the scheduler's stale-heartbeat sweep, which uses
	// ReasonHeartbeatLost. Event-log honesty.
	ReasonInfraReenqueue = TransitionReason{Kind: "infra_reenqueue"}
	ReasonPureCascade    = TransitionReason{Kind: "pure_cascade"}
	// ReasonDispatchImpossible transitions `stale → failed` directly when the
	// supervisor determines a node cannot be dispatched at all (e.g. the
	// template references an executor name not configured on any supervisor).
	// Unlike `ReasonPolicyGiveUp`, there is no policy chain involved — the
	// failure is infrastructural, not application-level, and the node never
	// entered `running`. Event log reflects the stale→failed transition
	// honestly.
	ReasonDispatchImpossible = TransitionReason{Kind: "dispatch_impossible"}

	// ReasonAcquirePass — stale → fresh, last_outcome=passed.
	// on_acquire_unavailable handler resolved pass; the node transitions
	// without invoking the executor and without firing the cascade.
	ReasonAcquirePass = TransitionReason{Kind: "acquire_pass"}

	// ReasonHandlerComplete — running → fresh.
	// on_executor_complete handler resolved. Subsumes
	// ReasonWorkCompleted for new code paths; the old constant is kept
	// as a deprecated alias for one cycle to ease the doc / annotation
	// migration.
	ReasonHandlerComplete = TransitionReason{Kind: "handler_complete"}

	// ReasonHandlerError — running → stale or running → failed.
	// on_executor_blocked / on_executor_errored handler routed
	// through error_types policy chain; specific transition follows
	// the policy outcome (retry → stale; invalidate → stale; give_up
	// → failed).
	//
	// NOTE: ReasonHandlerError is an audit-log marker only. NextState
	// does NOT accept it as a direct transition reason — the actual
	// state transition uses the policy-chain reasons (policy_retry /
	// policy_invalidate / policy_give_up) that already exist.
	ReasonHandlerError = TransitionReason{Kind: "handler_error"}

	// ReasonHandlerPass — running → fresh, last_outcome=passed.
	// on_executor_blocked / on_executor_errored handler resolved pass
	// (template explicitly opts to ignore the terminal).
	ReasonHandlerPass = TransitionReason{Kind: "handler_pass"}
)

// NextState returns the new state for a transition.
//
// @blessed-invariant (§17): NextState NEVER short-circuits when
// current == requested. Specifically `running → running` under reason
// `dispatch_claimed` returns ErrIllegalTransition. This is the load-bearing
// guard against double-execute. Any Go implementation that adds an
// idempotency optimization for "ergonomics" breaks the invariant.
// TS reference: rimsky/src/cell/state-machine.ts:37-73 (no from===to branch).
func NextState(current shared.NodeState, reason TransitionReason) (shared.NodeState, error) {
	switch current {
	case shared.NodeStateFresh:
		if reason.Kind == "invalidate_received" || reason.Kind == "operator_invalidate" {
			return shared.NodeStateStale, nil
		}
	case shared.NodeStateStale:
		if reason.Kind == "dispatch_claimed" {
			return shared.NodeStateRunning, nil
		}
		if reason.Kind == "pure_cascade" {
			return shared.NodeStateFresh, nil
		}
		if reason.Kind == "dispatch_impossible" {
			return shared.NodeStateFailed, nil
		}
		if reason.Kind == "acquire_pass" {
			return shared.NodeStateFresh, nil
		}
		// policy_give_up from stale supports on_acquire_unavailable:
		// { resolve: error } with an error_types[X].policy ending in
		// give_up. The node never entered running because the claim
		// returned Unavailable; the operator's policy decision is to
		// fail it permanently instead of retrying. Mirrors the
		// running → failed transition for the same reason kind.
		if reason.Kind == "policy_give_up" {
			return shared.NodeStateFailed, nil
		}
	case shared.NodeStateRunning:
		if reason.Kind == "work_completed" {
			return shared.NodeStateFresh, nil
		}
		if reason.Kind == "handler_complete" {
			return shared.NodeStateFresh, nil
		}
		if reason.Kind == "handler_pass" {
			return shared.NodeStateFresh, nil
		}
		// handler_error transitions follow the policy chain; expressed as
		// policy_retry / policy_invalidate / policy_give_up at the call site
		// after the policy chain resolves. ReasonHandlerError itself is not
		// a direct NextState input — it's the audit-log reason recorded
		// when a handler routes through error_types; the actual state
		// transition uses the policy-chain reasons that already exist.
		if reason.Kind == "policy_retry" ||
			reason.Kind == "policy_invalidate" ||
			reason.Kind == "heartbeat_lost" ||
			reason.Kind == "infra_reenqueue" {
			return shared.NodeStateStale, nil
		}
		if reason.Kind == "policy_give_up" {
			return shared.NodeStateFailed, nil
		}
	case shared.NodeStateFailed:
		if reason.Kind == "operator_reset" || reason.Kind == "operator_invalidate" {
			return shared.NodeStateStale, nil
		}
	}
	return "", fmt.Errorf("%w: from=%s reason=%s", shared.ErrIllegalTransition, current, reason.Kind)
}
