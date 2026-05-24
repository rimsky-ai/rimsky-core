// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cascade

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// allReasons enumerates every TransitionReason the state machine knows about.
// The table-driven test uses this to check every (from, reason) pair.
//
// ReasonPolicyInvalidate retired 2026-05-23 alongside the `invalidate`
// ErrorPolicy action; not in this list anymore.
var allReasons = []TransitionReason{
	ReasonInvalidateReceived,
	ReasonDispatchClaimed,
	ReasonPolicyRetry,
	ReasonPolicyGiveUp,
	ReasonOperatorReset,
	ReasonOperatorInvalidate,
	ReasonHeartbeatLost,
	ReasonInfraReenqueue,
	ReasonPureCascade,
	ReasonDispatchImpossible,
	ReasonAcquirePass,
	ReasonHandlerComplete,
	ReasonHandlerError,
	ReasonHandlerPass,
	ReasonHandlerPark,
	ReasonHandlerResume,
	ReasonParkTimeout,
	ReasonChildTransitioned,
	ReasonSubGraphInternalCascadeFired,
}

var allStates = []NodeState{
	NodeStateFresh,
	NodeStateStale,
	NodeStateRunning,
	NodeStateFailed,
	NodeStateParked,
}

// TestTransitionTable exhaustively checks every (from, reason) pair against
// the spec §4.1 table (plus the Go-port `pure_cascade` addition).
func TestTransitionTable(t *testing.T) {
	// valid[from][reason] = expected to-state
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
			// policy_give_up from stale supports
			// on_acquire_unavailable: { resolve: error } with
			// error_types[X].policy ending in give_up.
			"policy_give_up": NodeStateFailed,
		},
		NodeStateRunning: {
			"work_completed":   NodeStateFresh,
			"handler_complete": NodeStateFresh,
			"handler_pass":     NodeStateFresh,
			"policy_retry":     NodeStateStale,
			"heartbeat_lost":   NodeStateStale,
			"infra_reenqueue":  NodeStateStale,
			"policy_give_up":   NodeStateFailed,
			"handler_park":     NodeStateParked,
		},
		NodeStateFailed: {
			"operator_reset":      NodeStateStale,
			"operator_invalidate": NodeStateStale,
		},
		NodeStateParked: {
			"handler_resume": NodeStateStale,
			"park_timeout":   NodeStateFailed,
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

// TestRunningToRunningUnderDispatchClaimedIsRejected is the blessed invariant:
// the state machine MUST NOT short-circuit same-state transitions. A
// `dispatch_claimed` from running must fail so double-execute is impossible.
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
	for _, from := range []NodeState{NodeStateFresh, NodeStateRunning, NodeStateFailed} {
		_, err := NextState(from, ReasonDispatchImpossible)
		require.ErrorIs(t, err, ErrIllegalTransition, "from=%s", from)
	}
}

// TestPureCascadeOnlyValidFromStale confirms the Go-port addition: a pure
// cascade propagates stale → fresh without dispatch, and is illegal elsewhere.
func TestPureCascadeOnlyValidFromStale(t *testing.T) {
	got, err := NextState(NodeStateStale, ReasonPureCascade)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateRunning,
		NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonPureCascade)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// TestNextState_AcquirePass confirms stale → fresh under ReasonAcquirePass,
// illegal from other states.
func TestNextState_AcquirePass(t *testing.T) {
	got, err := NextState(NodeStateStale, ReasonAcquirePass)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateRunning,
		NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonAcquirePass)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// TestNextState_HandlerComplete confirms running → fresh under
// ReasonHandlerComplete, illegal from other states.
func TestNextState_HandlerComplete(t *testing.T) {
	got, err := NextState(NodeStateRunning, ReasonHandlerComplete)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateStale,
		NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerComplete)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// TestNextState_HandlerPass confirms running → fresh under ReasonHandlerPass,
// illegal from other states.
func TestNextState_HandlerPass(t *testing.T) {
	got, err := NextState(NodeStateRunning, ReasonHandlerPass)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateStale,
		NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerPass)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// TestNextState_HandlerErrorIsAuditOnly confirms ReasonHandlerError is not
// a direct NextState input from any state — it's an audit-log marker only.
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

// TestNextState_HandlerPark confirms running → parked under
// ReasonHandlerPark, illegal from other states.
func TestNextState_HandlerPark(t *testing.T) {
	got, err := NextState(NodeStateRunning, ReasonHandlerPark)
	require.NoError(t, err)
	require.Equal(t, NodeStateParked, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateStale,
		NodeStateFailed,
		NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerPark)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// TestNextState_HandlerResume confirms parked → stale under
// ReasonHandlerResume, illegal from other states.
func TestNextState_HandlerResume(t *testing.T) {
	got, err := NextState(NodeStateParked, ReasonHandlerResume)
	require.NoError(t, err)
	require.Equal(t, NodeStateStale, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateStale,
		NodeStateRunning,
		NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerResume)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// TestNextState_ParkTimeout confirms parked → failed under
// ReasonParkTimeout, illegal from other states.
func TestNextState_ParkTimeout(t *testing.T) {
	got, err := NextState(NodeStateParked, ReasonParkTimeout)
	require.NoError(t, err)
	require.Equal(t, NodeStateFailed, got)

	for _, from := range []NodeState{
		NodeStateFresh,
		NodeStateStale,
		NodeStateRunning,
		NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonParkTimeout)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// TestParkedToParkedRejected confirms the blessed-invariant-1 property
// (no same-state short-circuit) holds for parked under every transition
// reason — in particular parked → parked under any reason is illegal.
func TestParkedToParkedRejected(t *testing.T) {
	for _, reason := range allReasons {
		reason := reason
		t.Run(reason.Kind, func(t *testing.T) {
			got, err := NextState(NodeStateParked, reason)
			if got == NodeStateParked {
				t.Fatalf("parked → parked under reason %q must be rejected, got success", reason.Kind)
			}
			// handler_resume → running and park_timeout → failed are
			// the only legal exits from parked; everything else must
			// surface ErrIllegalTransition.
			if reason.Kind != "handler_resume" && reason.Kind != "park_timeout" {
				require.Error(t, err, "reason=%s", reason.Kind)
				require.True(t, errors.Is(err, ErrIllegalTransition))
			}
		})
	}
}

// TestNextStateParent_SubGraphInternalCascadeFired_RunningOnly confirms
// the subgraph_internal_cascade_fired reason is only legal from
// running. Per spec §State machine — sub-graph parents stay running
// while internal cascade fires.
func TestNextStateParent_SubGraphInternalCascadeFired_RunningOnly(t *testing.T) {
	t.Parallel()
	got, err := NextStateParent(NodeStateRunning, ReasonSubGraphInternalCascadeFired)
	require.NoError(t, err)
	require.Equal(t, NodeStateRunning, got)

	for _, from := range []NodeState{NodeStateFresh, NodeStateStale, NodeStateFailed, NodeStateParked} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextStateParent(from, ReasonSubGraphInternalCascadeFired)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrIllegalTransition))
		})
	}
}

// TestNextStateParent_ChildTransitioned_AggregateOK confirms the
// aggregation-OK sentinel is returned for every source state.
// Callers (state-propagation engine) compute the target state.
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

// TestNextStateParent_LeafReasonsStillRouteToNextState confirms
// non-parent-specific reasons (e.g., handler_complete, handler_park)
// continue to flow through the leaf transition table when called via
// NextStateParent.
func TestNextStateParent_LeafReasonsStillRouteToNextState(t *testing.T) {
	t.Parallel()
	// running → fresh under handler_complete (leaf path).
	got, err := NextStateParent(NodeStateRunning, ReasonHandlerComplete)
	require.NoError(t, err)
	require.Equal(t, NodeStateFresh, got)

	// running → parked under handler_park (leaf path).
	got, err = NextStateParent(NodeStateRunning, ReasonHandlerPark)
	require.NoError(t, err)
	require.Equal(t, NodeStateParked, got)

	// Illegal leaf transitions stay illegal under NextStateParent.
	_, err = NextStateParent(NodeStateRunning, ReasonDispatchClaimed)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrIllegalTransition))
}

// TestNextState_ChildTransitionedIsIllegalForLeafRuns confirms the
// new reason does NOT leak into NextState (leaf rows) — it's
// parent-only.
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
