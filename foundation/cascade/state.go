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
	case shared.NodeStateRunning:
		if reason.Kind == "work_completed" {
			return shared.NodeStateFresh, nil
		}
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
