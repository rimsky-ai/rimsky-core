// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cascade

import (
	"errors"
	"fmt"
)

type NodeState string

// @concept: node-run
const (
	NodeStatePending NodeState = "pending"
	NodeStateStale   NodeState = "stale"
	NodeStateRunning NodeState = "running"
	NodeStateHeld    NodeState = "held"
	NodeStateParked  NodeState = "parked"
	NodeStateFresh   NodeState = "fresh"
	NodeStateFailed  NodeState = "failed"
)

var ErrIllegalTransition = errors.New("illegal state transition")

// @concept: transition-reason
type TransitionReason struct {
	Kind string
}

var (
	ReasonGateCleared = TransitionReason{Kind: "gate_cleared"}

	ReasonDispatchClaimed    = TransitionReason{Kind: "dispatch_claimed"}
	ReasonPolicyGiveUp       = TransitionReason{Kind: "policy_give_up"}
	ReasonPureCascade        = TransitionReason{Kind: "pure_cascade"}
	ReasonDispatchImpossible = TransitionReason{Kind: "dispatch_impossible"}

	ReasonAcquirePass = TransitionReason{Kind: "acquire_pass"}

	ReasonHandlerComplete  = TransitionReason{Kind: "handler_complete"}
	ReasonHandlerHeld      = TransitionReason{Kind: "handler_held"}
	ReasonFanoutDispatched = TransitionReason{Kind: "fanout_dispatched"}
	ReasonHandlerPass      = TransitionReason{Kind: "handler_pass"}
	ReasonHandlerPark      = TransitionReason{Kind: "handler_park"}

	ReasonAutoTerminalCommit  = TransitionReason{Kind: "auto_terminal_commit"}
	ReasonAutoTerminalAbandon = TransitionReason{Kind: "auto_terminal_abandon"}

	ReasonDeadlineResume = TransitionReason{Kind: "deadline_resume"}

	ReasonCascadeResume = TransitionReason{Kind: "cascade_resume"}

	ReasonDispatchReleased = TransitionReason{Kind: "dispatch_released"}

	ReasonAggregateSettledSuccess = TransitionReason{Kind: "aggregate_settled_success"}
	ReasonAggregateSettledFailure = TransitionReason{Kind: "aggregate_settled_failure"}

	ReasonSubGraphInternalCascadeFired = TransitionReason{Kind: "subgraph_internal_cascade_fired"}

	ReasonInstanceKilled = TransitionReason{Kind: "instance_killed"}

	// @concept: cancel-siblings
	ReasonSiblingCancelled = TransitionReason{Kind: "sibling_cancelled"}
)

// @concept: frame
const SettlingSignalInstanceKilled = "terminal/error/" + ErrorClassInstanceKilled

const ErrorClassInstanceKilled = "instance_killed"

// @concept: cancel-siblings
const SettlingSignalSiblingFailed = "terminal/error/" + ErrorClassSiblingFailed

const ErrorClassSiblingFailed = "sibling_failed"

// @concept: node-run
// @decision: held-as-state-not-phase
// @decision: walker-rule-per-sender-node
func NextState(current NodeState, reason TransitionReason) (NodeState, error) {
	switch current {
	case NodeStatePending:
		switch reason.Kind {
		case ReasonGateCleared.Kind:
			return NodeStateStale, nil
		case ReasonInstanceKilled.Kind:
			return NodeStateFailed, nil
		case ReasonSiblingCancelled.Kind:
			return NodeStateFailed, nil
		}
	case NodeStateStale:
		switch reason.Kind {
		case ReasonDispatchClaimed.Kind:
			return NodeStateRunning, nil
		case ReasonPureCascade.Kind:
			return NodeStateFresh, nil
		case ReasonDispatchImpossible.Kind:
			return NodeStateFailed, nil
		case ReasonAcquirePass.Kind:
			return NodeStateFresh, nil
		case ReasonPolicyGiveUp.Kind:
			return NodeStateFailed, nil
		case ReasonInstanceKilled.Kind:
			return NodeStateFailed, nil
		case ReasonSiblingCancelled.Kind:
			return NodeStateFailed, nil
		case ReasonDispatchReleased.Kind:
			return NodeStateStale, nil
		}
	case NodeStateRunning:
		switch reason.Kind {
		case ReasonHandlerComplete.Kind:
			return NodeStateFresh, nil
		case ReasonHandlerHeld.Kind:
			return NodeStateHeld, nil
		case ReasonFanoutDispatched.Kind:
			return NodeStateHeld, nil
		case ReasonHandlerPass.Kind:
			return NodeStateFresh, nil
		case ReasonHandlerPark.Kind:
			return NodeStateParked, nil
		case ReasonPolicyGiveUp.Kind:
			return NodeStateFailed, nil
		case ReasonAutoTerminalAbandon.Kind:
			return NodeStateFailed, nil
		case ReasonInstanceKilled.Kind:
			return NodeStateFailed, nil
		case ReasonSiblingCancelled.Kind:
			return NodeStateFailed, nil
		case ReasonDispatchReleased.Kind:
			return NodeStateStale, nil
		}
	case NodeStateHeld:
		switch reason.Kind {
		case ReasonAutoTerminalCommit.Kind:
			return NodeStateFresh, nil
		case ReasonAutoTerminalAbandon.Kind:
			return NodeStateFailed, nil
		case ReasonInstanceKilled.Kind:
			return NodeStateFailed, nil
		case ReasonSiblingCancelled.Kind:
			return NodeStateFailed, nil
		case ReasonDispatchReleased.Kind:
			return NodeStateStale, nil
		}
	case NodeStateParked:
		switch reason.Kind {
		case ReasonDeadlineResume.Kind:
			return NodeStateStale, nil
		case ReasonCascadeResume.Kind:
			return NodeStateStale, nil
		case ReasonInstanceKilled.Kind:
			return NodeStateFailed, nil
		case ReasonSiblingCancelled.Kind:
			return NodeStateFailed, nil
		case ReasonDispatchReleased.Kind:
			return NodeStateStale, nil
		}
	}
	return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
}

// @concept: node-run
// @concept: transition-reason
func NextStateParent(current NodeState, reason TransitionReason) (NodeState, error) {
	switch reason.Kind {
	case ReasonAggregateSettledSuccess.Kind:
		if aggregatingParentState(current) {
			return NodeStateFresh, nil
		}
		return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
	case ReasonAggregateSettledFailure.Kind:
		if aggregatingParentState(current) {
			return NodeStateFailed, nil
		}
		return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
	case ReasonSubGraphInternalCascadeFired.Kind:
		if current == NodeStateRunning {
			return NodeStateRunning, nil
		}
		return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
	}
	return NextState(current, reason)
}

func aggregatingParentState(current NodeState) bool {
	switch current {
	case NodeStateStale, NodeStateRunning, NodeStateHeld, NodeStateFresh, NodeStateFailed:
		return true
	}
	return false
}

// @concept: transition-reason
func AggregateSettledReason(target NodeState) (TransitionReason, error) {
	switch target {
	case NodeStateFresh:
		return ReasonAggregateSettledSuccess, nil
	case NodeStateFailed:
		return ReasonAggregateSettledFailure, nil
	}
	return TransitionReason{}, fmt.Errorf("%w: aggregation settled on non-terminal state %s", ErrIllegalTransition, target)
}

// @concept: node-run
// @decision: walker-rule-per-sender-node
var InFlightStates = []NodeState{
	NodeStatePending,
	NodeStateStale,
	NodeStateRunning,
	NodeStateHeld,
	NodeStateParked,
}

// @concept: node-run
func IsInFlight(s NodeState) bool {
	switch s {
	case NodeStatePending, NodeStateStale, NodeStateRunning, NodeStateHeld, NodeStateParked:
		return true
	}
	return false
}

// @concept: node-run
func IsTerminal(s NodeState) bool {
	return s == NodeStateFresh || s == NodeStateFailed
}

// @concept: node-run
// @decision: non-cascade-direct-to-stale
type CreationReason string

const (
	CreationReasonCascade            CreationReason = "cascade"
	CreationReasonOperatorInvalidate CreationReason = "operator_invalidate"
	CreationReasonRecalculate        CreationReason = "recalculate"
	CreationReasonMessageDelivery    CreationReason = "message_delivery"
)
