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

	ReasonChildTransitioned = TransitionReason{Kind: "child_transitioned"}

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
		}
	}
	return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
}

// @concept: node-run
func NextStateParent(current NodeState, reason TransitionReason) (NodeState, error) {
	switch reason.Kind {
	case ReasonChildTransitioned.Kind:
		return "", &parentAggregateOK{From: current}
	case ReasonSubGraphInternalCascadeFired.Kind:
		if current == NodeStateRunning {
			return NodeStateRunning, nil
		}
		return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
	}
	return NextState(current, reason)
}

type parentAggregateOK struct {
	From NodeState
}

func (e *parentAggregateOK) Error() string {
	return fmt.Sprintf("parent aggregation in progress from=%s (caller chooses target)", e.From)
}

func IsParentAggregateOK(err error) bool {
	var pok *parentAggregateOK
	return errors.As(err, &pok)
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
