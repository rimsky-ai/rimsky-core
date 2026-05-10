// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SweepParkedNodes — time-based wake for parked rows whose resume_at
// has elapsed, plus the watchdog path for parked rows that overran
// max_park_duration_seconds. Called from the conductor tick at
// ~30-second cadence. Per the 2026-05-08 platform-extensions plan E3.
//
// For deadline-elapsed rows: routes through wakeParkedNode (the same
// shared helper used by G3's admin endpoint and H2's handler-emitted
// invalidates) so a single code path handles every parked-node wake.
//
// For overdue rows: emits an Errored verdict with
// error_class="park_timeout", which routes through the standard
// terminal-handler chain (applyTerminalAppError → on_executor_errored
// or default give_up policy).

package integration

import (
	"context"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// ParkedSweepArgs bundles the dependencies for SweepParkedNodes.
//
// ClaimHandles, ClaimHolders, AdvisoryLocker, and StoreRegistry are
// optional: when nil, the park_timeout watchdog still removes the
// worker_request row but cannot fire auto-terminal Abandon on held
// claims associated with the dispatch. Wiring all four enables the
// blessed-invariant 13 path described in failOverdueParkedRow.
type ParkedSweepArgs struct {
	Persist        persistence.Store
	Queue          persistence.Queue
	Clock          shared.Clock
	Logger         shared.Logger
	SupervisorID   string
	ClaimHandles   persistence.ClaimHandlesStore
	AdvisoryLocker persistence.AdvisoryLocker
	StoreRegistry  *locks.Registry
	// Limit caps the per-tick batch. Zero falls back to 100.
	Limit int
	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook (plan I3). Threaded into the deadline-elapsed wake invalidate
	// so `rimsky_invalidates_total` covers parked-resume sweeps. Nil →
	// no-op.
	Metrics MetricsHook
}

// SweepParkedNodes runs both the deadline-elapsed wake path and the
// max_park_duration overrun path on each tick. Failures on individual
// rows are logged and the loop continues.
func SweepParkedNodes(ctx context.Context, args ParkedSweepArgs) error {
	if args.Persist == nil || args.Queue == nil || args.SupervisorID == "" {
		return nil
	}
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	now := args.Clock.Now()
	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}

	// 1. Deadline-elapsed: resume_at has elapsed → wake via shared
	//    wakeParkedNode helper.
	ready, err := args.Queue.ListParkedReadyForResume(ctx, now, limit)
	if err != nil {
		log.Warn("SweepParkedNodes: ListParkedReadyForResume failed", "error", err.Error())
	}
	for _, row := range ready {
		ia := InvalidateArgs{
			Persist:      args.Persist,
			Queue:        args.Queue,
			Clock:        args.Clock,
			Logger:       log,
			TargetNodeID: row.NodeID,
			Reason:       "parked_resume_deadline_elapsed",
			SupervisorID: args.SupervisorID,
			Frame:        "next",
			Metrics:      args.Metrics,
		}
		if err := UnifiedInvalidate(ctx, ia, args.SupervisorID, WakeDeadlineElapsed); err != nil {
			log.Warn("SweepParkedNodes: wake failed",
				"node_id", row.NodeID.String(), "error", err.Error())
		}
	}

	// 2. Max-park-duration overrun: emit an Errored terminal via the
	//    standard pipeline. The watchdog forces parked → failed via the
	//    state machine's ReasonParkTimeout.
	overdue, err := args.Queue.ListParkedOverdue(ctx, now, limit)
	if err != nil {
		log.Warn("SweepParkedNodes: ListParkedOverdue failed", "error", err.Error())
	}
	for _, row := range overdue {
		if err := failOverdueParkedRow(ctx, args, row, log); err != nil {
			log.Warn("SweepParkedNodes: fail overdue failed",
				"node_id", row.NodeID.String(), "error", err.Error())
		}
	}
	return nil
}

// failOverdueParkedRow forces a parked node to failed with
// error_class="park_timeout". Before deleting the worker_request row it
// drives the held-claim Abandon path (blessed invariant 13): each
// rimsky_claim_handle row anchored on this node has its claim-holders
// rows marked 'failed', then ResolveClaimHandleTerminal fires Abandon
// on the producer. Without this, held claims would survive past the
// node's failure indefinitely (the orphan reaper would eventually
// delete the rimsky_claim_handle row, but no Abandon verb would ever
// fire — leaking producer-side state).
func failOverdueParkedRow(ctx context.Context, args ParkedSweepArgs, row persistence.ParkedRow, log shared.Logger) error {
	target, err := loadTargetNode(ctx, args.Persist, row.NodeID)
	if err != nil || target == nil {
		return err
	}
	// Transition via the state machine (parked → failed under
	// park_timeout). Use the Persist.Transaction so the queue removal,
	// state transition, held-claim Abandon, and audit-log are atomic.
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := args.Persist.Nodes().UpdateState(ctx, row.NodeID,
			shared.NodeStateFailed, cascade.ReasonParkTimeout, shared.LastOutcomeFailed, tx); err != nil {
			return err
		}
		// Auto-terminal Abandon for any held claims anchored on this
		// node. We mark the claim-holders rows as 'failed' so
		// CheckAndFireResolution computes the aggregate-failed → Abandon
		// outcome. Skipped when the wire-time wiring isn't complete
		// (ClaimHandles/StoreRegistry nil — typical of unit tests).
		if args.ClaimHandles != nil && args.StoreRegistry != nil {
			if err := abandonHeldClaimsForOverdueNode(ctx, args, tx, row.NodeID, log); err != nil {
				return err
			}
		}
		// Remove the worker_request row outright. With held claims
		// resolved above, no producer-side state is left dangling.
		if err := args.Queue.RemoveForNodeInTx(ctx, row.NodeID, "", tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &row.NodeID, InstanceID: &target.InstanceID,
			Kind: "park_timeout",
			Payload: map[string]any{
				"error_class":               "park_timeout",
				"reason_at_park":            row.Reason,
				"max_park_duration_seconds": row.MaxParkDurationSeconds,
				"parked_at":                 row.ParkedAt,
			},
		}, tx)
	})
}

// abandonHeldClaimsForOverdueNode marks every active claim-holders row
// for nodeID's claim-handle rows as 'failed' and fires
// ResolveClaimHandleTerminal per claim-handle to drive the Abandon
// verb + claim-handle delete. Runs inside the caller's tx.
func abandonHeldClaimsForOverdueNode(
	ctx context.Context, args ParkedSweepArgs, tx persistence.Tx,
	nodeID shared.UUID, log shared.Logger,
) error {
	handles, err := args.ClaimHandles.ListByHolderNode(ctx, nodeID, tx)
	if err != nil {
		return err
	}
	// Build a minimal RunArgs for the unified terminal-decision engine.
	runArgs := RunArgs{
		Persist:        args.Persist,
		Queue:          args.Queue,
		AdvisoryLocker: args.AdvisoryLocker,
		ClaimHandles:   args.ClaimHandles,
		StoreRegistry:  args.StoreRegistry,
		Clock:          args.Clock,
		Logger:         log,
		SupervisorID:   args.SupervisorID,
	}
	for _, h := range handles {
		if !h.IsHeld {
			// Non-held claims do not have claim-holders rows; their
			// terminal-decision is the active-terminal path on the
			// owning supervisor's runner. The orphan reaper handles
			// the cleanup since the owning worker_request is going away.
			continue
		}
		// Mark every still-active row as failed. ClaimHolders is reachable
		// via Persist; we don't take a separate field on args because the
		// dependency surface is already wide enough.
		if err := args.Persist.ClaimHolders().FailAllActiveByClaimHandle(ctx, h.ID, h.HolderSupervisorID, tx); err != nil {
			return err
		}
		// Fire CheckAndFireResolution. The claim-handle row's
		// HolderSupervisorID is the original acquirer; the resolution
		// engine compares it against runArgs.SupervisorID, which here is
		// the scheduler's supervisor id (different process). Override
		// runArgs.SupervisorID to the row's HolderSupervisorID for the
		// duration of this resolution so the claimant guard passes —
		// blessed invariant 13 says the auto-terminal flow runs against
		// the original holder. The scheduler is an authorized observer
		// fulfilling the cleanup that the original holder will never
		// perform (it was parked indefinitely).
		runArgs.SupervisorID = h.HolderSupervisorID
		if err := CheckAndFireResolution(ctx, runArgs, tx, h.ID); err != nil {
			return err
		}
	}
	return nil
}
