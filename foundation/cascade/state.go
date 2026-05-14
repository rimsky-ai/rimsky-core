// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cascade

import (
	"errors"
	"fmt"
)

// NodeState: fresh | stale | running | failed | parked
//
// `parked` is a non-terminal hold state distinct from `failed`. A node
// enters parked when its executor emits Park as a terminal event; it
// leaves parked via either time-based wake (SweepParkedNodes processes
// resume_at), in-graph or admin invalidate, or watchdog timeout
// (max_park_duration → failed). Cascade does NOT propagate from parked;
// held claims are retained across the park boundary; the orphan-claim
// reaper skips phase='parked' rows because heartbeating is paused
// during park.
type NodeState string

const (
	NodeStateFresh   NodeState = "fresh"
	NodeStateStale   NodeState = "stale"
	NodeStateRunning NodeState = "running"
	NodeStateFailed  NodeState = "failed"
	NodeStateParked  NodeState = "parked"
)

// LastOutcome is the resolution flavor recorded on rimsky_nodes for
// terminal-for-this-frame transitions. Distinct from NodeState; the
// node's state machine is unchanged. last_outcome lives on the
// rimsky_nodes row alongside state and is written by the same
// transition that lands the node in fresh or failed.
//
// Values are persisted as TEXT under both Postgres and SQLite. NULL
// means "no outcome recorded yet" (legacy fresh nodes pre-migration).
//
// See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §2.2.
type LastOutcome string

const (
	LastOutcomeFreshChanged   LastOutcome = "fresh_changed"
	LastOutcomeFreshUnchanged LastOutcome = "fresh_unchanged"
	LastOutcomePassed         LastOutcome = "passed"
	LastOutcomePureCascade    LastOutcome = "pure_cascade"
	LastOutcomeFailed         LastOutcome = "failed"
)

// ErrIllegalTransition is the sentinel returned by NextState when a state
// transition is not in the spec §4.1 transition table. blessed-invariant
// (§17): NextState never silently accepts an illegal transition.
var ErrIllegalTransition = errors.New("illegal state transition")

// @concept: transition-reason
//
// Audit-grade enum carried on every node-state transition. Sibling to
// `last_outcome` (see `.ok-planner/design/concepts/transition-reason.md`
// Relationship section for the pairing table). The cascade-fire predicate
// reads `last_outcome`, not `transition_reason` — the two enums describe
// different facets of the same transition.
//
// TransitionReason identifies WHY a state transition was requested. Preserved
// from TS v1's discriminated union plus the Go port's `pure_cascade` addition.
type TransitionReason struct {
	Kind string
}

var (
	ReasonInvalidateReceived = TransitionReason{Kind: "invalidate_received"}
	ReasonDispatchClaimed    = TransitionReason{Kind: "dispatch_claimed"}
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

	// ReasonAcquirePass — stale → fresh, last_outcome=passed.
	// on_acquire_unavailable handler resolved pass; the node transitions
	// without invoking the executor and without firing the cascade.
	ReasonAcquirePass = TransitionReason{Kind: "acquire_pass"}

	// ReasonHandlerComplete — running → fresh. Fired when the
	// on_executor_complete handler resolves the terminal verdict.
	ReasonHandlerComplete = TransitionReason{Kind: "handler_complete"}

	// ReasonHandlerError — RESERVED NEGATIVE. The state machine
	// deliberately does NOT accept this reason as a direct transition
	// trigger; the on_executor_errored handler must route through the
	// error_types policy chain and emit one of policy_retry /
	// policy_invalidate / policy_give_up.
	//
	// The constant exists so this rejection is encoded in the
	// transition-reason vocabulary and pinned by a negative test
	// (`TestNextState_HandlerErrorIsAuditOnly` — name predates this
	// docstring rewrite). A code change that tries to use this reason
	// as a NextState input fails closed at the test, not silently in
	// production.
	//
	// No production code path emits this reason; if you find yourself
	// wanting to, the right move is to add a policy outcome that maps
	// to one of the existing accepted reasons, not to relax NextState.
	ReasonHandlerError = TransitionReason{Kind: "handler_error"}

	// ReasonHandlerPass — running → fresh, last_outcome=passed.
	// on_executor_errored handler resolved pass (template explicitly
	// opts to ignore the terminal).
	ReasonHandlerPass = TransitionReason{Kind: "handler_pass"}

	// ReasonHandlerPark — running → parked.
	// Executor emitted Park as its terminal event. The node's
	// held claims are retained across the park boundary. See section B
	// of .ok-planner/plans/2026-05-08-platform-extensions-for-agent-consumers.md.
	ReasonHandlerPark = TransitionReason{Kind: "handler_park"}

	// ReasonHandlerResume — parked → stale.
	// SweepParkedNodes (deadline_elapsed) or an external invalidate
	// (admin endpoint or in-graph on_event) resumed a parked node.
	// The node transitions to stale so the standard
	// SelectCandidates → atomic-acquisition → transitionToRunning path
	// re-dispatches with ResumeContext populated from the persisted
	// park metadata; rimsky's wake supervisor doesn't need to be the
	// one that runs the resume.
	ReasonHandlerResume = TransitionReason{Kind: "handler_resume"}

	// ReasonParkTimeout — parked → failed.
	// The watchdog observed parked_at + max_park_duration ≤ now and
	// transitioned the node to failed with last_outcome=failed and
	// error_class="park_timeout".
	ReasonParkTimeout = TransitionReason{Kind: "park_timeout"}
)

// NextState returns the new state for a transition.
//
// @blessed-invariant (§17): NextState NEVER short-circuits when
// current == requested. Specifically `running → running` under reason
// `dispatch_claimed` returns ErrIllegalTransition. This is the load-bearing
// guard against double-execute. Any Go implementation that adds an
// idempotency optimization for "ergonomics" breaks the invariant.
// TS reference: rimsky/src/cell/state-machine.ts:37-73 (no from===to branch).
//
// @blessed-invariant 1 (post-Phase-6): the five legitimate states are
// fresh, stale, running, failed, parked. The legitimate transitions
// involving parked are: running → parked under handler_park; parked →
// stale under handler_resume (the wake transitions to stale so the
// standard SelectCandidates → atomic-acquisition → transitionToRunning
// path re-dispatches); parked → failed under park_timeout. All other
// transitions involving parked (including parked → fresh, parked →
// running directly, parked → parked) are illegal.
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
		// policy_give_up from stale supports on_acquire_unavailable:
		// { resolve: error } with an error_types[X].policy ending in
		// give_up. The node never entered running because the claim
		// returned Unavailable; the operator's policy decision is to
		// fail it permanently instead of retrying. Mirrors the
		// running → failed transition for the same reason kind.
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
		// handler_park transitions running → parked. The held claim is
		// retained across the park boundary; the orphan-claim reaper
		// skips phase='parked' rows.
		if reason.Kind == "handler_park" {
			return NodeStateParked, nil
		}
		// handler_error transitions follow the policy chain; expressed as
		// policy_retry / policy_invalidate / policy_give_up at the call site
		// after the policy chain resolves. ReasonHandlerError itself is
		// NOT a direct NextState input — see its docstring for why it's
		// reserved-negative.
		if reason.Kind == "policy_retry" ||
			reason.Kind == "policy_invalidate" ||
			reason.Kind == "heartbeat_lost" ||
			reason.Kind == "infra_reenqueue" {
			return NodeStateStale, nil
		}
		if reason.Kind == "policy_give_up" {
			return NodeStateFailed, nil
		}
	case NodeStateFailed:
		if reason.Kind == "operator_reset" || reason.Kind == "operator_invalidate" {
			return NodeStateStale, nil
		}
	case NodeStateParked:
		// Parked nodes leave only via resume (deadline-elapsed wake or
		// external invalidate) or via watchdog timeout. parked → fresh
		// is explicitly rejected — a parked node re-enters work via
		// the standard dispatch path (parked → stale → claim →
		// running). The handler_resume reason routes through stale
		// rather than directly to running so the wake supervisor (which
		// may not be one running an executor pool, e.g. control-api)
		// doesn't have to run the dispatch — the next supervisor
		// tick's SelectCandidates picks up the stale row and runs the
		// standard atomic-acquisition path.
		if reason.Kind == "handler_resume" {
			return NodeStateStale, nil
		}
		if reason.Kind == "park_timeout" {
			return NodeStateFailed, nil
		}
	}
	return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
}
