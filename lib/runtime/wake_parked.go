// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Parked-resume wake: `wakeParkedNode` transitions a parked node back to
// stale, runs the cascade walk so downstream subscribers gate on it, and
// emits the parked-resume audit event. Called by the deadline-elapsed
// sweep (E3, `code:sweep_parked.go::SweepParkedNodes`) when `resume_at`
// elapses.
//
// The operator-API `node:invalidate` route retired with the 2026-06-14
// message-schema-layer reshape — there is no longer a "unified
// invalidate" dispatch surface that fans out by node state, because the
// operator-side path goes through typed messages now. The parked-resume
// wake is the only remaining caller of this helper.
//
// `wakeParkedReceiverInTx` (further below) is the in-tx variant used by
// the cascade walker when it visits a parked downstream receiver during
// a sender's terminal cascade.

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// WakeReason captures the invalidate's source for the resume_reason
// field of ResumeContext. Distinct values let the executor log / tune
// behavior on resume.
type WakeReason string

const (
	// WakeDeadlineElapsed is set by SweepParkedNodes when resume_at
	// elapses.
	WakeDeadlineElapsed WakeReason = "deadline_elapsed"
	// WakeExternalInvalidate identifies a wake driven by an external
	// signal rather than the parked-resume deadline. Used today by
	// scenario harness helpers exercising the parked-resume path
	// without ticking the deadline; distinct from WakeDeadlineElapsed
	// so resume-time bookkeeping can tell a deadline-driven wake apart
	// from an external-driven one.
	WakeExternalInvalidate WakeReason = "external_invalidate"
)

// WakeParkedArgs bundles the persistence + bookkeeping inputs for
// `WakeParkedNode`. The parked-resume sweep is the only caller today
// (`code:sweep_parked.go::SweepParkedNodes`); future internal callers
// pass the same handle set.
type WakeParkedArgs struct {
	Persist      persistence.Tables
	Queue        persistence.Queue
	Logger       shared.Logger
	TargetNodeID shared.UUID
	SupervisorID string
}

// WakeParkedNode wakes the parked node identified by `TargetNodeID`.
// Loads the target row to verify it is parked + resolve its scope, then
// hands off to the in-tx wake helper. Returns nil silently when the
// target is not found, is not parked, or has no projected RunScope (the
// only states the parked-resume sweep encounters).
//
//	@concept: parked-state
func WakeParkedNode(ctx context.Context, args WakeParkedArgs, reason WakeReason) error {
	if args.Persist == nil {
		return errors.New("WakeParkedNode: Persist required")
	}
	if args.Queue == nil {
		return errors.New("WakeParkedNode: Queue required")
	}
	if args.SupervisorID == "" {
		return errors.New("WakeParkedNode: SupervisorID required")
	}
	target, err := loadTargetNode(ctx, args.Persist, args.TargetNodeID)
	if err != nil {
		return err
	}
	if target == nil {
		if args.Logger != nil {
			args.Logger.Debug("WakeParkedNode: target not found",
				"node_id", args.TargetNodeID.String())
		}
		return nil
	}
	if target.State != cascade.NodeStateParked {
		// @constraint: target has already moved out of parked (raced
		// with another caller). Silent no-op: the parked-resume sweep
		// iterates over a snapshot of parked rows and tolerates such
		// races.
		return nil
	}
	return wakeParkedNode(ctx, args, target, reason)
}

// wakeParkedNode runs the parked-node wake. Transitions phase
// parked→pending (claimed_by reset to NULL so any eligible supervisor
// can pick the row up; the supervisorID is recorded in the audit-log
// row only), transitions node state parked→stale, audit-logs the
// resume.
//
// The actual re-dispatch happens on the next supervisor tick — the
// runner's SelectCandidates picks up phase='pending' rows and the
// standard ready sweep re-enqueues stale nodes.
func wakeParkedNode(ctx context.Context, args WakeParkedArgs, target *persistence.NodeRow, reason WakeReason) error {
	// @constraint: target.RunScopeID (when set) addresses the specific
	// parked run row via its RunScope. Without one, no in-flight row
	// exists for this node — nothing to wake.
	if target.RunScopeID == nil {
		return nil
	}
	targetRunScopeID := *target.RunScopeID
	parked, err := args.Queue.GetParkedByNode(ctx, target.ID, targetRunScopeID)
	if err != nil {
		return fmt.Errorf("wakeParkedNode: GetParkedByNode: %w", err)
	}
	if parked == nil {
		// @constraint: race — the node is parked but the node-run row
		// has been reaped or the parked status is in transition.
		if args.Logger != nil {
			args.Logger.Warn("wakeParkedNode: no parked node-run row found",
				"node_id", target.ID.String())
		}
		return nil
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		resumed, err := args.Queue.ResumeParkedInTx(ctx, tx, parked.DispatchID, string(reason))
		if err != nil {
			return err
		}
		if !resumed {
			return nil
		}
		// @constraint: thread targetRunScopeID so fan-out children's
		// parked → stale transition lands on this run row rather than a
		// sibling.
		if err := args.Persist.Nodes().UpdateState(ctx, target.ID, targetRunScopeID,
			cascade.NodeStateStale, cascade.ReasonHandlerResume, nil, tx); err != nil {
			return err
		}
		if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &target.ID, InstanceID: &target.InstanceID,
			Kind: events.KindParkedResumeStarted(),
			Payload: map[string]any{
				"resume_reason": string(reason),
				"supervisor_id": args.SupervisorID,
				"prior_reason":  parked.Reason,
				"had_session":   parked.SessionToken != "",
				"payload_bytes": len(parked.PayloadInline) + len(parked.PayloadHandle),
			},
		}, tx); err != nil {
			return err
		}
		// @constraint: pessimistic-invalidate per spec Piece 1 — parked →
		// stale is the sender's invalidation in this frame (parked is
		// settled). Gate downstream subscribers so they don't dispatch
		// with stale upstream data while the woken sender re-runs.
		//
		//	@concept: cascade
		//	@concept: wait-set
		if target.FrameID == nil {
			return nil
		}
		return walkCascadeForInvalidatedNode(ctx, args.Persist, args.Queue, tx,
			args.Logger, target.ID, target.InstanceID, *target.FrameID)
	})
}

// wakeParkedReceiverInTx is the cascade-walk variant of wakeParkedNode:
// runs inside the caller's tx (no nested transaction), drives the same
// parked → stale transition + audit event, stamps the receiver
// with the sender's frame_id on BOTH `rimsky_nodes.frame_id` and
// `rimsky_node_runs.frame_id` so the receiver joins the active frame
// (rather than waiting for a separate next-frame open).
//
// Used by `cascadeSubscribersStaleInTx` when the cascade walk visits a
// parked receiver, and by `pullForceRefreshUpstreams` when an
// upstream-refresh upstream is parked. The parent caller (the cascade
// walk) then inserts the wait-set row that gates the newly-stale
// receiver on the invalidating sender.
//
// Frame stamp: After `ResumeParkedInTx` transitions the run row from
// `parked` to `pending`, an in-flight run row exists for this node,
// so `MarkStaleForCascade`'s NOT EXISTS guard rejects and it does NOT
// touch `rimsky_nodes.frame_id`. `SetFrameID` performs the
// unconditional stamp directly. Without it, the eligibility-predicate
// JOIN (`w.frame_id = n.frame_id` in `ListReadyForDispatch` /
// `ListPureCascadeReady`) cannot find the new-frame wait-set row the
// cascade walker just inserted, the gate is bypassed, and the
// parked-woken receiver dispatches without waiting on its sender.
// `RebindRunFrameInTx` pairs the run-row side: without it, the
// receiver-side `GetInFlightRunForNode(node, newFrameID)` returns
// `hasRun=false` (its WHERE clause excludes the still-prior-frame run),
// so the wait-set blocker the caller would insert never installs.
// Threading both into this primitive ensures every caller (standard
// cascade-subscription path AND hard-dep pull path) gets the stamps
// without each site re-implementing them.
//
//	@concept: parked-state
//	@concept: cascade
func wakeParkedReceiverInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	receiver persistence.NodeRow, frameID shared.UUID,
) error {
	return wakeParkedReceiverWithDepsInTx(ctx, args.Persist, args.Queue, tx, receiver, frameID)
}

// wakeParkedReceiverWithDepsInTx is the persist+queue-direct variant of
// wakeParkedReceiverInTx. Both the standard runner_terminal.go cascade
// walk (via wakeParkedReceiverInTx) and the message-virtual-node settle
// in message_delivery.go (which runs from a scheduler-tick context that
// has no RunArgs to thread) call this directly so the parked-wake
// branch cannot drift between the two cascade walkers.
//
//	@concept: parked-state
//	@concept: cascade
func wakeParkedReceiverWithDepsInTx(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue, tx persistence.Tx,
	receiver persistence.NodeRow, frameID shared.UUID,
) error {
	// @constraint: under RunScope-first the parked-row resolver keys on
	// (node_id, run_scope_id). Without a projected RunScope on the
	// receiver, no parked row can exist for this node — return early.
	if receiver.RunScopeID == nil {
		return nil
	}
	receiverRunScopeID := *receiver.RunScopeID
	parked, err := queue.GetParkedByNode(ctx, receiver.ID, receiverRunScopeID)
	if err != nil {
		return fmt.Errorf("wakeParkedReceiverInTx: GetParkedByNode: %w", err)
	}
	if parked == nil {
		// @constraint: race — the receiver is parked but the node-run row
		// is in transition. The cascade walker's MarkStaleForCascade path
		// is the Phase B recovery surface; bail out cleanly here.
		return nil
	}
	resumed, err := queue.ResumeParkedInTx(ctx, tx, parked.DispatchID, "cascade_wake")
	if err != nil {
		return fmt.Errorf("wakeParkedReceiverInTx: ResumeParkedInTx: %w", err)
	}
	// @constraint: stamp node.frame_id unconditionally. After
	// ResumeParkedInTx transitioned the parked run to pending, an
	// in-flight run row exists for this node, so MarkStaleForCascade's
	// NOT EXISTS guard rejects and it does NOT touch node.frame_id —
	// using SetFrameID directly is the stamp the eligibility-predicate
	// JOIN (`w.frame_id = n.frame_id`) requires. Without it, the
	// wait-set blocker the cascade walker inserts at the new frame is
	// invisible to ListReadyForDispatch and the woken receiver dispatches
	// without waiting.
	if err := persist.Nodes().SetFrameID(ctx, receiver.ID, &frameID, tx); err != nil {
		return fmt.Errorf("wakeParkedReceiverInTx: stamp node.frame_id: %w", err)
	}
	// @constraint: rebind the resumed run row's frame_id so receiver-side
	// GetInFlightRunForNode(node, frameID) resolves it. Done before the
	// !resumed early-return so the (rare) raced-into-pending case
	// (deadline sweep concurrently transitioned parked → pending) also
	// gets the rebind — that scenario leaves an in-flight pending row
	// at the prior parked frame that still needs migration.
	if err := queue.RebindRunFrameInTx(ctx, tx, parked.DispatchID, frameID); err != nil {
		// @constraint: tolerate ErrRunRowMissing on the !resumed branch
		// — the row may have been hard-deleted by orphan-reaper between
		// our GetParkedByNode read and now. The MarkStaleForCascade call
		// below recovers by inserting a fresh pending stale row.
		if resumed || !errors.Is(err, persistence.ErrRunRowMissing) {
			return fmt.Errorf("wakeParkedReceiverInTx: rebind run frame: %w", err)
		}
	}
	if !resumed {
		// @constraint: already moved out of parked (raced with the
		// deadline sweep or another cascade); skip UpdateState and the
		// resume event to avoid double-firing. Phase B's cascade
		// allocator handles the race-recovery affirm+mark sequence.
		return nil
	}
	// @constraint: thread receiverRunScopeID so the parked → stale
	// transition disambiguates fan-out siblings.
	if err := persist.Nodes().UpdateState(ctx, receiver.ID, receiverRunScopeID,
		cascade.NodeStateStale, cascade.ReasonHandlerResume, nil, tx); err != nil {
		return fmt.Errorf("wakeParkedReceiverInTx: UpdateState: %w", err)
	}
	return persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &receiver.ID, InstanceID: &receiver.InstanceID,
		Kind: events.KindParkedResumeStarted(),
		Payload: map[string]any{
			"resume_reason": "cascade_wake",
			"prior_reason":  parked.Reason,
			"had_session":   parked.SessionToken != "",
			"payload_bytes": len(parked.PayloadInline) + len(parked.PayloadHandle),
		},
	}, tx)
}

// loadTargetNode wraps Persist.Transaction + Nodes().Get for the
// state-dispatch read.
func loadTargetNode(ctx context.Context, persist persistence.Tables, id shared.UUID) (*persistence.NodeRow, error) {
	var out *persistence.NodeRow
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := persist.Nodes().Get(ctx, id, tx)
		out = row
		return err
	})
	return out, err
}
