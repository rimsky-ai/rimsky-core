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

// ErrIllegalTransition is the sentinel returned by NextState when a state
// transition is not in the spec §4.1 transition table. blessed-invariant
// (§17): NextState never silently accepts an illegal transition.
var ErrIllegalTransition = errors.New("illegal state transition")

// @concept: transition-reason
//
// Audit-grade enum carried on every node-state transition. Identifies
// WHY a state transition was requested. The cascade-fire predicate is
// purely subscriber-driven post-2026-05-23 (subscription-edge match +
// CEL `when:` predicate over the emitted signal); `transition_reason`
// is no longer consulted by cascade-fire and lives strictly as audit.
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
	// ReasonPolicyInvalidate retired 2026-05-23 alongside the
	// `invalidate` ErrorPolicy action (retired 2026-05-14). The 4-value
	// ErrorPolicy vocabulary (pass | give_up | retry |
	// discard_claims_then_retry) has no invalidate verb; receivers
	// declare cascade coupling via SubscriptionEntry.
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

	// ReasonAcquirePass — stale → fresh, settling_signal_type
	// carries the canonical `terminal/error/<class>` envelope.
	// Fired when the operator's `error_types:` chain for the synthetic
	// `acquire/unavailable` class resolves `pass` (pre-dispatch
	// acquisition failure absolved by template policy); the node
	// transitions without invoking the executor and without firing
	// the cascade.
	ReasonAcquirePass = TransitionReason{Kind: "acquire_pass"}

	// ReasonHandlerComplete — running → fresh. Fired by the runtime
	// after the executor's Success terminal verdict has been applied
	// (the pre-2026-05-23 lifecycle-handler slot retired; the
	// transition is now driven directly by the terminal-handler in
	// `runtime/runner_terminal.go::applyTerminalComplete`).
	ReasonHandlerComplete = TransitionReason{Kind: "handler_complete"}

	// ReasonHandlerError — RESERVED NEGATIVE. The state machine
	// deliberately does NOT accept this reason as a direct transition
	// trigger; an executor Error terminal must route through the
	// operator's `error_types:` policy chain and resolve to one of
	// policy_retry / policy_give_up.
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

	// ReasonHandlerPass — running → fresh, settling_signal_type
	// carries the canonical `terminal/error/<class>` envelope. Fired
	// when the operator's `error_types:` chain for an executor Error
	// resolves `pass` (template explicitly opts to ignore the
	// terminal). The chain advances so a subsequent same-class error
	// doesn't pass again.
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
	// transitioned the node to failed with
	// settling_signal_type=terminal/error/park_timeout (carrying
	// error_class="park_timeout" in the payload).
	ReasonParkTimeout = TransitionReason{Kind: "park_timeout"}

	// ReasonChildTransitioned — parent-run-only. Fired by the
	// state-propagation engine in runtime/state_propagation.go when a
	// child run transitions and the parent re-aggregates. Allowed
	// transitions under this reason are constrained to parent rows
	// (rimsky_node_runs.parent_run_id IS NULL is FALSE for the child's
	// parent), but the state machine cannot inspect persistence — the
	// caller is responsible for restricting NextStateParent to parent
	// rows. Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §State machine.
	ReasonChildTransitioned = TransitionReason{Kind: "child_transitioned"}

	// ReasonSubGraphInternalCascadeFired — parent-run-only. Fired when
	// a sub-graph's entry node completed (success terminal) and the
	// parent stays running while internal cascade dispatches the
	// internal child nodes. Same parent-row restriction as
	// ReasonChildTransitioned.
	ReasonSubGraphInternalCascadeFired = TransitionReason{Kind: "subgraph_internal_cascade_fired"}

	// ReasonInstanceKilled — forced instance teardown. Drives a
	// resource-holding non-terminal node-run (running | parked) → failed
	// when an operator force-terminates the instance. State-machine-
	// validation-only: NOT emitted as an audit-event kind (the teardown's
	// audit identity is the `instance_terminated` event-log row written by
	// the control handler).
	ReasonInstanceKilled = TransitionReason{Kind: "instance_killed"}
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
// path re-dispatches); parked → failed under park_timeout; parked →
// failed under instance_killed (forced instance teardown force-fails a
// parked node-run, which retains its held claim across the park
// boundary). All other transitions involving parked (including parked →
// fresh, parked → running directly, parked → parked) are illegal.
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
		// policy_retry / policy_give_up at the call site after the
		// policy chain resolves. ReasonHandlerError itself is NOT a
		// direct NextState input — see its docstring for why it's
		// reserved-negative. The `policy_invalidate` reason retired
		// 2026-05-23 alongside the `invalidate` ErrorPolicy action.
		if reason.Kind == "policy_retry" ||
			reason.Kind == "heartbeat_lost" ||
			reason.Kind == "infra_reenqueue" {
			return NodeStateStale, nil
		}
		if reason.Kind == "policy_give_up" {
			return NodeStateFailed, nil
		}
		// instance_killed force-fails a running node-run during forced
		// instance teardown (covers the await_async-stuck case too —
		// such a run is still `running` and holds its claim).
		if reason.Kind == "instance_killed" {
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
		// instance_killed force-fails a parked node-run during forced
		// instance teardown. A parked node retains its held claim across
		// the park boundary, so it is resource-holding and must be torn
		// down too.
		if reason.Kind == "instance_killed" {
			return NodeStateFailed, nil
		}
	}
	return "", fmt.Errorf("%w: from=%s reason=%s", ErrIllegalTransition, current, reason.Kind)
}

// NextStateParent is the parent-run-only state machine. It accepts the
// leaf-run transition table (delegates to NextState) PLUS the
// parent-run-only transitions described in spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §State machine:
//
//   - terminal → stale (re-trigger of a parent run after frame
//     restart): from fresh|failed under invalidate_received |
//     operator_invalidate. Already legal under NextState.
//   - terminal → running (parent's first child began running for a
//     new frame): from fresh|failed under child_transitioned.
//   - running → running (state-aggregation re-fires while children
//     are still in flight; the parent stays running because the
//     aggregation rule produced no new terminal): under
//     child_transitioned or subgraph_internal_cascade_fired.
//   - running → stale (parent re-marked stale by child cascade that
//     fires invalidate-style on the parent before any child went
//     terminal): under child_transitioned. Permitted because
//     aggregation may yield stale.
//   - running → fresh / failed / parked (parent terminal, computed
//     by aggregation): under child_transitioned. The leaf transition
//     table already allows running → fresh (handler_complete) and
//     running → failed (policy_give_up); for parents the same final
//     states are reached via aggregation, not the executor handler.
//
// Callers MUST restrict invocations of NextStateParent to rows known
// to be parent runs (rimsky_node_runs.parent_run_id IS NULL is FALSE
// for some child). NextState (the leaf-run variant) preserves the
// strict leaf-only transition table.
//
// @blessed-invariant 1: the state machine never silently accepts an
// illegal transition. For parent rows, NextStateParent extends the
// allowed set as documented above; for leaf rows, NextState is
// unchanged.
func NextStateParent(current NodeState, reason TransitionReason) (NodeState, error) {
	switch reason.Kind {
	case "child_transitioned":
		// Parent re-aggregates from child state. The new state is
		// determined by the aggregation rule in runtime; the state
		// machine's contract is to accept any of the legitimate
		// parent target states from any of the legitimate parent
		// source states. The state-propagation engine writes the
		// computed state directly; the machine permits it.
		switch current {
		case NodeStateFresh, NodeStateFailed:
			// Permit fresh/failed → running (new frame starting) or
			// fresh/failed → stale (re-trigger).
			return "", &parentAggregateOK{From: current}
		case NodeStateStale, NodeStateRunning:
			// Permit running → running | fresh | failed | parked | stale.
			// Permit stale → running | fresh | failed | parked.
			return "", &parentAggregateOK{From: current}
		case NodeStateParked:
			// Parked → running is permitted when an external
			// invalidate or wake fires for the parent; otherwise
			// illegal.
			return "", &parentAggregateOK{From: current}
		}
	case "subgraph_internal_cascade_fired":
		// Only valid when parent is running.
		if current == NodeStateRunning {
			return NodeStateRunning, nil
		}
	}
	// Fall through to the standard leaf transition table for the
	// remaining reasons. (Parent runs share all leaf transitions
	// except they're broader on the reasons above.)
	return NextState(current, reason)
}

// parentAggregateOK is a sentinel error type returned by
// NextStateParent when the caller is expected to choose the target
// state itself (the aggregation engine computes it; the state
// machine permits the write). Callers detect via errors.As.
type parentAggregateOK struct {
	From NodeState
}

func (e *parentAggregateOK) Error() string {
	return fmt.Sprintf("parent aggregation in progress from=%s (caller chooses target)", e.From)
}

// IsParentAggregateOK reports whether err is the sentinel returned by
// NextStateParent when the aggregation engine is responsible for
// choosing the parent's new state. Callers that receive this error
// MUST themselves validate the chosen target is one of {stale,
// running, fresh, failed, parked} before writing.
func IsParentAggregateOK(err error) bool {
	var pok *parentAggregateOK
	return errors.As(err, &pok)
}
