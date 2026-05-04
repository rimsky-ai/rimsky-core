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
}

var allStates = []shared.NodeState{
	shared.NodeStateFresh,
	shared.NodeStateStale,
	shared.NodeStateRunning,
	shared.NodeStateFailed,
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
		},
		shared.NodeStateRunning: {
			"work_completed":    shared.NodeStateFresh,
			"policy_retry":      shared.NodeStateStale,
			"policy_invalidate": shared.NodeStateStale,
			"heartbeat_lost":    shared.NodeStateStale,
			"infra_reenqueue":   shared.NodeStateStale,
			"policy_give_up":    shared.NodeStateFailed,
		},
		shared.NodeStateFailed: {
			"operator_reset":      shared.NodeStateStale,
			"operator_invalidate": shared.NodeStateStale,
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
