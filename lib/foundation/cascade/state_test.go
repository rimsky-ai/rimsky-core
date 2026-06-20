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
	ReasonInvalidateReceived,
	ReasonDispatchClaimed,
	ReasonPolicyRetry,
	ReasonPolicyGiveUp,
	ReasonOperatorReset,
	ReasonOperatorInvalidate,
	ReasonInfraReenqueue,
	ReasonPureCascade,
	ReasonDispatchImpossible,
	ReasonAcquirePass,
	ReasonHandlerComplete,
	ReasonHandlerError,
	ReasonHandlerPass,
	ReasonHandlerPark,
	ReasonHandlerResume,
	ReasonDeadlineResume,
	ReasonParkTimeout,
	ReasonChildTransitioned,
	ReasonSubGraphInternalCascadeFired,
	ReasonInstanceKilled,
}

var allStates = []NodeState{
	NodeStateFresh,
	NodeStateStale,
	NodeStateRunning,
	NodeStateFailed,
	NodeStateParked,
	NodeStateResuming,
}

func TestTransitionTable(t *testing.T) {
	valid := map[NodeState]map[string]NodeState{
		NodeStateFresh: {
			"invalidate_received": NodeStateStale,
			"operator_invalidate": NodeStateStale,
		},
		NodeStateStale: {
			"dispatch_claimed":    NodeStateRunning,
			"pure_cascade":        NodeStateFresh,
			"dispatch_impossible": NodeStateFailed,
			"acquire_pass":        NodeStateFresh,
			"policy_give_up":      NodeStateFailed,
		},
		NodeStateRunning: {
			"handler_complete": NodeStateFresh,
			"handler_pass":     NodeStateFresh,
			"policy_retry":     NodeStateStale,
			"infra_reenqueue":  NodeStateStale,
			"policy_give_up":   NodeStateFailed,
			"handler_park":     NodeStateParked,
			"instance_killed":  NodeStateFailed,
		},
		NodeStateFailed: {
			"operator_reset":      NodeStateStale,
			"operator_invalidate": NodeStateStale,
		},
		NodeStateParked: {
			"deadline_resume": NodeStateResuming,
			"handler_resume":  NodeStateStale,
			"park_timeout":    NodeStateFailed,
			"instance_killed": NodeStateFailed,
		},
		NodeStateResuming: {
			"dispatch_claimed":    NodeStateRunning,
			"policy_give_up":      NodeStateFailed,
			"dispatch_impossible": NodeStateFailed,
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
	for _, from := range []NodeState{NodeStateFresh, NodeStateRunning, NodeStateFailed, NodeStateParked} {
		_, err := NextState(from, ReasonDispatchImpossible)
		require.ErrorIs(t, err, ErrIllegalTransition, "from=%s", from)
	}
	got, err := NextState(NodeStateResuming, ReasonDispatchImpossible)
	require.NoError(t, err)
	require.Equal(t, NodeStateFailed, got)
}

func TestPureCascadeOnlyValidFromStale(t *testing.T) {
	got, err := NextState(NodeStateStale, ReasonPureCascade)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateRunning,
		NodeStateFailed,
		NodeStateParked,
		NodeStateResuming,
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
		NodeStateFresh,
		NodeStateRunning,
		NodeStateFailed,
		NodeStateParked,
		NodeStateResuming,
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
		NodeStateFresh,
		NodeStateStale,
		NodeStateFailed,
		NodeStateParked,
		NodeStateResuming,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerComplete)
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
		NodeStateFresh,
		NodeStateStale,
		NodeStateFailed,
		NodeStateParked,
		NodeStateResuming,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerPass)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_HandlerErrorIsAuditOnly(t *testing.T) {
	for _, from := range allStates {
		from := from
		t.Run(string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerError)
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
		NodeStateFresh,
		NodeStateStale,
		NodeStateFailed,
		NodeStateParked,
		NodeStateResuming,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerPark)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_HandlerResume(t *testing.T) {
	got, err := NextState(NodeStateParked, ReasonHandlerResume)
	require.NoError(t, err)
	require.Equal(t, NodeStateStale, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateStale,
		NodeStateRunning,
		NodeStateFailed,
		NodeStateResuming,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerResume)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_DeadlineResume(t *testing.T) {
	got, err := NextState(NodeStateParked, ReasonDeadlineResume)
	require.NoError(t, err)
	require.Equal(t, NodeStateResuming, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateStale,
		NodeStateRunning,
		NodeStateFailed,
		NodeStateResuming,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonDeadlineResume)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextState_Resuming_DispatchClaimed(t *testing.T) {
	got, err := NextState(NodeStateResuming, ReasonDispatchClaimed)
	require.NoError(t, err)
	require.Equal(t, NodeStateRunning, got)
}

func TestNextState_ParkTimeout(t *testing.T) {
	got, err := NextState(NodeStateParked, ReasonParkTimeout)
	require.NoError(t, err)
	require.Equal(t, NodeStateFailed, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateStale,
		NodeStateRunning,
		NodeStateFailed,
		NodeStateResuming,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonParkTimeout)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestParkedToParkedRejected(t *testing.T) {
	for _, reason := range allReasons {
		reason := reason
		t.Run(reason.Kind, func(t *testing.T) {
			got, err := NextState(NodeStateParked, reason)
			if got == NodeStateParked {
				t.Fatalf("parked → parked under reason %q must be rejected, got success", reason.Kind)
			}
			if reason.Kind != "handler_resume" && reason.Kind != "deadline_resume" &&
				reason.Kind != "park_timeout" && reason.Kind != "instance_killed" {
				require.Error(t, err, "reason=%s", reason.Kind)
				require.True(t, errors.Is(err, ErrIllegalTransition))
			}
		})
	}
}

func TestNextStateParent_SubGraphInternalCascadeFired_RunningOnly(t *testing.T) {
	t.Parallel()
	got, err := NextStateParent(NodeStateRunning, ReasonSubGraphInternalCascadeFired)
	require.NoError(t, err)
	require.Equal(t, NodeStateRunning, got)

	for _, from := range []NodeState{NodeStateFresh, NodeStateStale, NodeStateFailed, NodeStateParked, NodeStateResuming} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextStateParent(from, ReasonSubGraphInternalCascadeFired)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

func TestNextStateParent_ChildTransitioned_AggregateOK(t *testing.T) {
	t.Parallel()
	for _, from := range allStates {
		from := from
		t.Run(string(from), func(t *testing.T) {
			_, err := NextStateParent(from, ReasonChildTransitioned)
			require.Error(t, err)
			require.True(t, IsParentAggregateOK(err),
				"expected aggregate-OK sentinel, got %v", err)
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
