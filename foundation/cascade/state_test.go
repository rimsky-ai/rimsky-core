// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cascade

import (
	"errors"
	"testing"

	"github.com/fallguy/rimsky/modeling/shared"
	"github.com/stretchr/testify/require"
)

// allReasons enumerates every TransitionReason the state machine knows about.
// The table-driven test uses this to check every (from, reason) pair.
var allReasons = []TransitionReason{
	ReasonInvalidateReceived,
	ReasonDispatchClaimed,
	ReasonWorkCompleted,
	ReasonPolicyRetry,
	ReasonPolicyInvalidate,
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
}

var allStates = []shared.NodeState{
	shared.NodeStateFresh,
	shared.NodeStateStale,
	shared.NodeStateRunning,
	shared.NodeStateFailed,
	shared.NodeStateParked,
}

// TestTransitionTable exhaustively checks every (from, reason) pair against
// the spec §4.1 table (plus the Go-port `pure_cascade` addition).
func TestTransitionTable(t *testing.T) {
	// valid[from][reason] = expected to-state
	valid := map[shared.NodeState]map[string]shared.NodeState{
		shared.NodeStateFresh: {
			"invalidate_received": shared.NodeStateStale,
			"operator_invalidate": shared.NodeStateStale,
		},
		shared.NodeStateStale: {
			"dispatch_claimed":    shared.NodeStateRunning,
			"pure_cascade":        shared.NodeStateFresh,
			"dispatch_impossible": shared.NodeStateFailed,
			"acquire_pass":        shared.NodeStateFresh,
			// policy_give_up from stale supports
			// on_acquire_unavailable: { resolve: error } with
			// error_types[X].policy ending in give_up.
			"policy_give_up": shared.NodeStateFailed,
		},
		shared.NodeStateRunning: {
			"work_completed":    shared.NodeStateFresh,
			"handler_complete":  shared.NodeStateFresh,
			"handler_pass":      shared.NodeStateFresh,
			"policy_retry":      shared.NodeStateStale,
			"policy_invalidate": shared.NodeStateStale,
			"heartbeat_lost":    shared.NodeStateStale,
			"infra_reenqueue":   shared.NodeStateStale,
			"policy_give_up":    shared.NodeStateFailed,
			"handler_park":      shared.NodeStateParked,
		},
		shared.NodeStateFailed: {
			"operator_reset":      shared.NodeStateStale,
			"operator_invalidate": shared.NodeStateStale,
		},
		shared.NodeStateParked: {
			"handler_resume": shared.NodeStateStale,
			"park_timeout":   shared.NodeStateFailed,
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
					require.True(t, errors.Is(err, shared.ErrIllegalTransition),
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
	got, err := NextState(shared.NodeStateRunning, ReasonDispatchClaimed)
	require.Error(t, err)
	require.True(t, errors.Is(err, shared.ErrIllegalTransition))
	require.Equal(t, shared.NodeState(""), got)
}

func TestDispatchImpossibleTransitionsStaleToFailed(t *testing.T) {
	t.Parallel()
	got, err := NextState(shared.NodeStateStale, ReasonDispatchImpossible)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got)
}

func TestDispatchImpossibleRejectedFromNonStale(t *testing.T) {
	t.Parallel()
	for _, from := range []shared.NodeState{shared.NodeStateFresh, shared.NodeStateRunning, shared.NodeStateFailed} {
		_, err := NextState(from, ReasonDispatchImpossible)
		require.ErrorIs(t, err, shared.ErrIllegalTransition, "from=%s", from)
	}
}

// TestPureCascadeOnlyValidFromStale confirms the Go-port addition: a pure
// cascade propagates stale → fresh without dispatch, and is illegal elsewhere.
func TestPureCascadeOnlyValidFromStale(t *testing.T) {
	got, err := NextState(shared.NodeStateStale, ReasonPureCascade)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got)

	for _, from := range []shared.NodeState{
		shared.NodeStateFresh,
		shared.NodeStateRunning,
		shared.NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonPureCascade)
			require.Error(t, err)
			require.True(t, errors.Is(err, shared.ErrIllegalTransition))
		})
	}
}

// TestNextState_AcquirePass confirms stale → fresh under ReasonAcquirePass,
// illegal from other states.
func TestNextState_AcquirePass(t *testing.T) {
	got, err := NextState(shared.NodeStateStale, ReasonAcquirePass)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got)

	for _, from := range []shared.NodeState{
		shared.NodeStateFresh,
		shared.NodeStateRunning,
		shared.NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonAcquirePass)
			require.Error(t, err)
			require.True(t, errors.Is(err, shared.ErrIllegalTransition))
		})
	}
}

// TestNextState_HandlerComplete confirms running → fresh under
// ReasonHandlerComplete, illegal from other states.
func TestNextState_HandlerComplete(t *testing.T) {
	got, err := NextState(shared.NodeStateRunning, ReasonHandlerComplete)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got)

	for _, from := range []shared.NodeState{
		shared.NodeStateFresh,
		shared.NodeStateStale,
		shared.NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerComplete)
			require.Error(t, err)
			require.True(t, errors.Is(err, shared.ErrIllegalTransition))
		})
	}
}

// TestNextState_HandlerPass confirms running → fresh under ReasonHandlerPass,
// illegal from other states.
func TestNextState_HandlerPass(t *testing.T) {
	got, err := NextState(shared.NodeStateRunning, ReasonHandlerPass)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got)

	for _, from := range []shared.NodeState{
		shared.NodeStateFresh,
		shared.NodeStateStale,
		shared.NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerPass)
			require.Error(t, err)
			require.True(t, errors.Is(err, shared.ErrIllegalTransition))
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
			require.True(t, errors.Is(err, shared.ErrIllegalTransition))
		})
	}
}

// TestNextState_HandlerPark confirms running → parked under
// ReasonHandlerPark, illegal from other states.
func TestNextState_HandlerPark(t *testing.T) {
	got, err := NextState(shared.NodeStateRunning, ReasonHandlerPark)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateParked, got)

	for _, from := range []shared.NodeState{
		shared.NodeStateFresh,
		shared.NodeStateStale,
		shared.NodeStateFailed,
		shared.NodeStateParked,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerPark)
			require.Error(t, err)
			require.True(t, errors.Is(err, shared.ErrIllegalTransition))
		})
	}
}

// TestNextState_HandlerResume confirms parked → stale under
// ReasonHandlerResume, illegal from other states.
func TestNextState_HandlerResume(t *testing.T) {
	got, err := NextState(shared.NodeStateParked, ReasonHandlerResume)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got)

	for _, from := range []shared.NodeState{
		shared.NodeStateFresh,
		shared.NodeStateStale,
		shared.NodeStateRunning,
		shared.NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonHandlerResume)
			require.Error(t, err)
			require.True(t, errors.Is(err, shared.ErrIllegalTransition))
		})
	}
}

// TestNextState_ParkTimeout confirms parked → failed under
// ReasonParkTimeout, illegal from other states.
func TestNextState_ParkTimeout(t *testing.T) {
	got, err := NextState(shared.NodeStateParked, ReasonParkTimeout)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got)

	for _, from := range []shared.NodeState{
		shared.NodeStateFresh,
		shared.NodeStateStale,
		shared.NodeStateRunning,
		shared.NodeStateFailed,
	} {
		from := from
		t.Run("illegal/"+string(from), func(t *testing.T) {
			_, err := NextState(from, ReasonParkTimeout)
			require.Error(t, err)
			require.True(t, errors.Is(err, shared.ErrIllegalTransition))
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
			got, err := NextState(shared.NodeStateParked, reason)
			if got == shared.NodeStateParked {
				t.Fatalf("parked → parked under reason %q must be rejected, got success", reason.Kind)
			}
			// handler_resume → running and park_timeout → failed are
			// the only legal exits from parked; everything else must
			// surface ErrIllegalTransition.
			if reason.Kind != "handler_resume" && reason.Kind != "park_timeout" {
				require.Error(t, err, "reason=%s", reason.Kind)
				require.True(t, errors.Is(err, shared.ErrIllegalTransition))
			}
		})
	}
}

// TestLastOutcomeStringSerialization protects the column-value contract:
// LastOutcome constants must serialize to the documented strings.
func TestLastOutcomeStringSerialization(t *testing.T) {
	require.Equal(t, "fresh_changed", string(shared.LastOutcomeFreshChanged))
	require.Equal(t, "fresh_unchanged", string(shared.LastOutcomeFreshUnchanged))
	require.Equal(t, "passed", string(shared.LastOutcomePassed))
	require.Equal(t, "pure_cascade", string(shared.LastOutcomePureCascade))
	require.Equal(t, "failed", string(shared.LastOutcomeFailed))
}
