// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cascade

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

var allReasons = []TransitionReason{
	ReasonGateCleared,
	ReasonDispatchClaimed,
	ReasonPolicyGiveUp,
	ReasonPureCascade,
	ReasonDispatchImpossible,
	ReasonAcquirePass,
	ReasonHandlerComplete,
	ReasonHandlerHeld,
	ReasonFanoutDispatched,
	ReasonHandlerPass,
	ReasonHandlerPark,
	ReasonAutoTerminalCommit,
	ReasonAutoTerminalAbandon,
	ReasonDeadlineResume,
	ReasonDispatchReleased,
	ReasonAggregateSettledSuccess,
	ReasonAggregateSettledFailure,
	ReasonInstanceKilled,
	ReasonSiblingCancelled,
}

var allStates = []NodeState{
	NodeStatePending,
	NodeStateStale,
	NodeStateRunning,
	NodeStateHeld,
	NodeStateParked,
	NodeStateFresh,
	NodeStateFailed,
}

func TestTransitionTable(t *testing.T) {
	valid := map[NodeState]map[string]NodeState{
		NodeStatePending: {
			"gate_cleared":      NodeStateStale,
			"instance_killed":   NodeStateFailed,
			"sibling_cancelled": NodeStateFailed,
		},
		NodeStateStale: {
			"dispatch_claimed":    NodeStateRunning,
			"pure_cascade":        NodeStateFresh,
			"dispatch_impossible": NodeStateFailed,
			"acquire_pass":        NodeStateFresh,
			"policy_give_up":      NodeStateFailed,
			"instance_killed":     NodeStateFailed,
			"sibling_cancelled":   NodeStateFailed,
			"dispatch_released":   NodeStateStale,
		},
		NodeStateRunning: {
			"handler_complete":      NodeStateFresh,
			"handler_held":          NodeStateHeld,
			"fanout_dispatched":     NodeStateHeld,
			"handler_pass":          NodeStateFresh,
			"policy_give_up":        NodeStateFailed,
			"handler_park":          NodeStateParked,
			"auto_terminal_abandon": NodeStateFailed,
			"instance_killed":       NodeStateFailed,
			"sibling_cancelled":     NodeStateFailed,
			"dispatch_released":     NodeStateStale,
		},
		NodeStateHeld: {
			"auto_terminal_commit":  NodeStateFresh,
			"auto_terminal_abandon": NodeStateFailed,
			"instance_killed":       NodeStateFailed,
			"sibling_cancelled":     NodeStateFailed,
			"dispatch_released":     NodeStateStale,
		},
		NodeStateParked: {
			"deadline_resume":   NodeStateStale,
			"instance_killed":   NodeStateFailed,
			"sibling_cancelled": NodeStateFailed,
			"dispatch_released": NodeStateStale,
		},
	}

	for _, from := range allStates {
		for _, reason := range allReasons {
			t.Run(string(from)+"/"+reason.Kind, func(t *testing.T) {
				got, err := NextState(from, reason)
				if want, ok := valid[from][reason.Kind]; ok {
					require.NoError(t, err)
					require.Equal(t, want, got)
				} else {
					require.Error(t, err)
					require.True(t, errors.Is(err, ErrIllegalTransition),
						"expected ErrIllegalTransition, got %v", err)
					require.Equal(t, NodeState(""), got, "an illegal transition returns no state")
				}
			})
		}
	}
}

func TestNextState_ParkTimeoutRetired(t *testing.T) {
	for _, from := range allStates {
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, TransitionReason{Kind: "park_timeout"})
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestInFlightAndTerminalHelpers(t *testing.T) {
	t.Parallel()
	inFlight := map[NodeState]bool{
		NodeStatePending: true,
		NodeStateStale:   true,
		NodeStateRunning: true,
		NodeStateHeld:    true,
		NodeStateParked:  true,
	}
	for _, s := range allStates {
		require.Equal(t, inFlight[s], IsInFlight(s), "IsInFlight(%s)", s)
		require.Equal(t, !inFlight[s], IsTerminal(s), "IsTerminal(%s)", s)
	}
}

func TestAggregateSettledReasonNamesTheReasonForEachTerminalTarget(t *testing.T) {
	t.Parallel()
	reason, err := AggregateSettledReason(NodeStateFresh)
	require.NoError(t, err)
	require.Equal(t, ReasonAggregateSettledSuccess, reason)

	reason, err = AggregateSettledReason(NodeStateFailed)
	require.NoError(t, err)
	require.Equal(t, ReasonAggregateSettledFailure, reason)

	for _, s := range InFlightStates {
		_, err := AggregateSettledReason(s)
		require.ErrorIs(t, err, ErrIllegalTransition,
			"aggregation settling on the non-terminal state %s has no reason", s)
	}
}

func TestNextStateParent_SubgraphInternalCascadeFired_RunningStaysRunning(t *testing.T) {
	t.Parallel()
	got, err := NextStateParent(NodeStateRunning, ReasonSubGraphInternalCascadeFired)
	require.NoError(t, err)
	require.Equal(t, NodeStateRunning, got)
	for _, from := range allStates {
		if from == NodeStateRunning {
			continue
		}
		t.Run(string(from), func(t *testing.T) {
			_, err := NextStateParent(from, ReasonSubGraphInternalCascadeFired)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition),
				"subgraph_internal_cascade_fired is only legal from running, got %v", err)
		})
	}
}

func TestNextStateParent_LeafReasonsStillRouteToNextState(t *testing.T) {
	t.Parallel()
	got, err := NextStateParent(NodeStateRunning, ReasonHandlerComplete)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	got, err = NextStateParent(NodeStateRunning, ReasonHandlerPark)
	require.NoError(t, err)
	require.Equal(t, NodeStateParked, got)

	_, err = NextStateParent(NodeStateRunning, ReasonDispatchClaimed)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrIllegalTransition))
}

func TestNextStateParent_AggregateSettlementNeedsAParentThatCanCarryChildren(t *testing.T) {
	t.Parallel()
	targets := map[string]NodeState{
		ReasonAggregateSettledSuccess.Kind: NodeStateFresh,
		ReasonAggregateSettledFailure.Kind: NodeStateFailed,
	}
	for _, reason := range []TransitionReason{ReasonAggregateSettledSuccess, ReasonAggregateSettledFailure} {
		for _, from := range []NodeState{NodeStateStale, NodeStateRunning, NodeStateHeld, NodeStateFresh, NodeStateFailed} {
			t.Run(reason.Kind+"/admits/"+string(from), func(t *testing.T) {
				got, err := NextStateParent(from, reason)
				require.NoError(t, err)
				require.Equal(t, targets[reason.Kind], got)
			})
		}
		for _, from := range []NodeState{NodeStatePending, NodeStateParked} {
			t.Run(reason.Kind+"/refuses/"+string(from), func(t *testing.T) {
				_, err := NextStateParent(from, reason)
				require.ErrorIs(t, err, ErrIllegalTransition)
			})
		}
	}
}
