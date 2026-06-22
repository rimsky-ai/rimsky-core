// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	ReasonParkTimeout,
	ReasonChildTransitioned,
	ReasonInstanceKilled,
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
			"gate_cleared":    NodeStateStale,
			"instance_killed": NodeStateFailed,
		},
		NodeStateStale: {
			"dispatch_claimed":    NodeStateRunning,
			"pure_cascade":        NodeStateFresh,
			"dispatch_impossible": NodeStateFailed,
			"acquire_pass":        NodeStateFresh,
			"policy_give_up":      NodeStateFailed,
			"instance_killed":     NodeStateFailed,
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
		},
		NodeStateHeld: {
			"auto_terminal_commit":  NodeStateFresh,
			"auto_terminal_abandon": NodeStateFailed,
			"instance_killed":       NodeStateFailed,
		},
		NodeStateParked: {
			"deadline_resume": NodeStateStale,
			"park_timeout":    NodeStateFailed,
			"instance_killed": NodeStateFailed,
		},
	}

	for _, from := range allStates {
		for _, reason := range allReasons {
			from, reason := from, reason
			t.Run(string(from)+"/"+reason.Kind, func(t *testing.T) {
				got, err := NextState(from, reason)
				if want, ok := valid[from][reason.Kind]; ok {
					require.NoError(t, err)
					require.Equal(t, want, got)
				} else {
					require.Error(t, err)
					require.True(t, errors.Is(err, ErrIllegalTransition),
						"expected ErrIllegalTransition, got %v", err)
				}
			})
		}
	}
}

func TestFreshAndFailedAreTerminal(t *testing.T) {
	t.Parallel()
	for _, from := range []NodeState{NodeStateFresh, NodeStateFailed} {
		from := from
		t.Run(string(from), func(t *testing.T) {
			for _, reason := range allReasons {
				_, err := NextState(from, reason)
				require.ErrorIs(t, err, ErrIllegalTransition,
					"terminal state %s must have no outgoing transitions; reason=%s", from, reason.Kind)
			}
		})
	}
}

func TestRunningToRunningUnderDispatchClaimedIsRejected(t *testing.T) {
	got, err := NextState(NodeStateRunning, ReasonDispatchClaimed)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrIllegalTransition))
	require.Equal(t, NodeState(""), got)
}

func TestDispatchImpossibleTransitionsStaleToFailed(t *testing.T) {
	t.Parallel()
	got, err := NextState(NodeStateStale, ReasonDispatchImpossible)
	require.NoError(t, err)
	require.Equal(t, NodeStateFailed, got)
}

func TestDispatchImpossibleRejectedFromNonStale(t *testing.T) {
	t.Parallel()
	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateRunning, NodeStateHeld, NodeStateFailed, NodeStateParked,
	} {
		_, err := NextState(from, ReasonDispatchImpossible)
		require.ErrorIs(t, err, ErrIllegalTransition, "from=%s", from)
	}
}

func TestPureCascadeOnlyValidFromStale(t *testing.T) {
	got, err := NextState(NodeStateStale, ReasonPureCascade)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateRunning, NodeStateHeld, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonPureCascade)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_AcquirePass(t *testing.T) {
	got, err := NextState(NodeStateStale, ReasonAcquirePass)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateRunning, NodeStateHeld, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonAcquirePass)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_HandlerComplete(t *testing.T) {
	got, err := NextState(NodeStateRunning, ReasonHandlerComplete)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateStale, NodeStateHeld, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerComplete)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_HandlerHeld(t *testing.T) {
	got, err := NextState(NodeStateRunning, ReasonHandlerHeld)
	require.NoError(t, err)
	require.Equal(t, NodeStateHeld, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateStale, NodeStateHeld, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerHeld)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_AutoTerminalCommit(t *testing.T) {
	got, err := NextState(NodeStateHeld, ReasonAutoTerminalCommit)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateStale, NodeStateRunning, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonAutoTerminalCommit)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_AutoTerminalAbandon(t *testing.T) {
	got, err := NextState(NodeStateHeld, ReasonAutoTerminalAbandon)
	require.NoError(t, err)
	require.Equal(t, NodeStateFailed, got)

	got, err = NextState(NodeStateRunning, ReasonAutoTerminalAbandon)
	require.NoError(t, err)
	require.Equal(t, NodeStateFailed, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateStale, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonAutoTerminalAbandon)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_HandlerPass(t *testing.T) {
	got, err := NextState(NodeStateRunning, ReasonHandlerPass)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateStale, NodeStateHeld, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerPass)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_HandlerPark(t *testing.T) {
	got, err := NextState(NodeStateRunning, ReasonHandlerPark)
	require.NoError(t, err)
	require.Equal(t, NodeStateParked, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateStale, NodeStateHeld, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerPark)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// @story: resume-preserves-snapshot
func TestNextState_DeadlineResume(t *testing.T) {
	got, err := NextState(NodeStateParked, ReasonDeadlineResume)
	require.NoError(t, err)
	require.Equal(t, NodeStateStale, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateStale, NodeStateRunning, NodeStateHeld, NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonDeadlineResume)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_GateCleared(t *testing.T) {
	got, err := NextState(NodeStatePending, ReasonGateCleared)
	require.NoError(t, err)
	require.Equal(t, NodeStateStale, got)

	for _, from := range []NodeState{
		NodeStateFresh, NodeStateStale, NodeStateRunning, NodeStateHeld, NodeStateFailed, NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonGateCleared)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_ParkTimeout(t *testing.T) {
	got, err := NextState(NodeStateParked, ReasonParkTimeout)
	require.NoError(t, err)
	require.Equal(t, NodeStateFailed, got)

	for _, from := range []NodeState{
		NodeStatePending, NodeStateFresh, NodeStateStale, NodeStateRunning, NodeStateHeld, NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonParkTimeout)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestParkedTransitionsWhitelist(t *testing.T) {
	allowed := map[string]NodeState{
		"deadline_resume": NodeStateStale,
		"park_timeout":    NodeStateFailed,
		"instance_killed": NodeStateFailed,
	}
	for _, reason := range allReasons {
		reason := reason
		t.Run(reason.Kind, func(t *testing.T) {
			got, err := NextState(NodeStateParked, reason)
			want, ok := allowed[reason.Kind]
			if !ok {
				require.Error(t, err, "reason=%s", reason.Kind)
				require.True(t, errors.Is(err, ErrIllegalTransition))
				return
			}
			require.NoError(t, err)
			require.Equal(t, want, got)
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
	require.True(t, IsSerializationGated(NodeStateRunning))
	require.True(t, IsSerializationGated(NodeStateHeld))
	require.True(t, IsSerializationGated(NodeStateParked))
	require.False(t, IsSerializationGated(NodeStatePending))
	require.False(t, IsSerializationGated(NodeStateStale))
	require.False(t, IsSerializationGated(NodeStateFresh))
	require.False(t, IsSerializationGated(NodeStateFailed))
}

func TestNextStateParent_ChildTransitioned_AggregateOK(t *testing.T) {
	t.Parallel()
	inFlight := map[NodeState]bool{
		NodeStatePending: true,
		NodeStateStale:   true,
		NodeStateRunning: true,
		NodeStateHeld:    true,
		NodeStateParked:  true,
	}
	for _, from := range allStates {
		from := from
		t.Run(string(from), func(t *testing.T) {
			_, err := NextStateParent(from, ReasonChildTransitioned)
			require.Error(t, err)
			if inFlight[from] {
				require.True(t, IsParentAggregateOK(err),
					"expected aggregate-OK sentinel for in-flight parent, got %v", err)
				return
			}
			require.True(t, errors.Is(err, ErrIllegalTransition),
				"expected ErrIllegalTransition for terminal parent, got %v", err)
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

func TestNextState_ChildTransitionedIsIllegalForLeafRuns(t *testing.T) {
	t.Parallel()
	for _, from := range allStates {
		from := from
		t.Run(string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonChildTransitioned)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}
