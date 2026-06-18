// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cascade

import (
	"errors"
	"fmt"
)

type NodeState string

const (
	NodeStateFresh   NodeState = "fresh"
	NodeStateStale   NodeState = "stale"
	NodeStateRunning NodeState = "running"
	NodeStateFailed  NodeState = "failed"
	NodeStateParked  NodeState = "parked"
)

var ErrIllegalTransition = errors.New("illegal state transition")

// @concept: transition-reason
type TransitionReason struct {
	Kind string
}

var (
	ReasonInvalidateReceived = TransitionReason{Kind: "invalidate_received"}
	ReasonDispatchClaimed    = TransitionReason{Kind: "dispatch_claimed"}
	ReasonPolicyRetry        = TransitionReason{Kind: "policy_retry"}
	ReasonPolicyGiveUp       = TransitionReason{Kind: "policy_give_up"}
	ReasonOperatorReset      = TransitionReason{Kind: "operator_reset"}
	ReasonOperatorInvalidate = TransitionReason{Kind: "operator_invalidate"}
	ReasonInfraReenqueue     = TransitionReason{Kind: "infra_reenqueue"}
	ReasonPureCascade        = TransitionReason{Kind: "pure_cascade"}
	ReasonDispatchImpossible = TransitionReason{Kind: "dispatch_impossible"}

	ReasonAcquirePass = TransitionReason{Kind: "acquire_pass"}

	ReasonHandlerComplete = TransitionReason{Kind: "handler_complete"}

	ReasonHandlerError = TransitionReason{Kind: "handler_error"}

	ReasonHandlerPass = TransitionReason{Kind: "handler_pass"}

	ReasonHandlerPark = TransitionReason{Kind: "handler_park"}

	ReasonHandlerResume = TransitionReason{Kind: "handler_resume"}

	ReasonParkTimeout = TransitionReason{Kind: "park_timeout"}

	ReasonChildTransitioned = TransitionReason{Kind: "child_transitioned"}

	ReasonSubGraphInternalCascadeFired = TransitionReason{Kind: "subgraph_internal_cascade_fired"}

	ReasonInstanceKilled = TransitionReason{Kind: "instance_killed"}
)

func NextState(current NodeState, reason TransitionReason) (NodeState, error) {
	switch current {
	case NodeStateFresh:
		if reason.Kind == "invalidate_received" || reason.Kind == "operator_invalidate" {
			return NodeStateStale, nil
		}
	case NodeStateStale:
		if reason.Kind == "dispatch_claimed" {
			return NodeStateRunning, nil
		}
		if reason.Kind == "pure_cascade" {
			return NodeStateFresh, nil
		}
		if reason.Kind == "dispatch_impossible" {
			return NodeStateFailed, nil
		}
		if reason.Kind == "acquire_pass" {
			return NodeStateFresh, nil
		}
		if reason.Kind == "policy_give_up" {
			return NodeStateFailed, nil
		}
	case NodeStateRunning:
		if reason.Kind == "handler_complete" {
			return NodeStateFresh, nil
		}
		if reason.Kind == "handler_pass" {
			return NodeStateFresh, nil
		}
		if reason.Kind == "handler_park" {
			return NodeStateParked, nil
		}
		if reason.Kind == "policy_retry" ||
			reason.Kind == "infra_reenqueue" {
			return NodeStateStale, nil
		}
		if reason.Kind == "policy_give_up" {
			return NodeStateFailed, nil
		}
		if reason.Kind == "instance_killed" {
			return NodeStateFailed, nil
		}
	case NodeStateFailed:
		if reason.Kind == "operator_reset" || reason.Kind == "operator_invalidate" {
			return NodeStateStale, nil
		}
	case NodeStateParked:
		if reason.Kind == "handler_resume" {
			return NodeStateStale, nil
		}
		if reason.Kind == "park_timeout" {
			return NodeStateFailed, nil
		}
		if reason.Kind == "instance_killed" {
			return NodeStateFailed, nil
		}
	}
	return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
}

func NextStateParent(current NodeState, reason TransitionReason) (NodeState, error) {
	switch reason.Kind {
	case "child_transitioned":
		switch current {
		case NodeStateFresh, NodeStateFailed:
			return "", &parentAggregateOK{From: current}
		case NodeStateStale, NodeStateRunning:
			return "", &parentAggregateOK{From: current}
		case NodeStateParked:
			return "", &parentAggregateOK{From: current}
		}
	case "subgraph_internal_cascade_fired":
		if current == NodeStateRunning {
			return NodeStateRunning, nil
		}
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
