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
// For overdue rows: emits an Error{error_class:"park_timeout"} verdict,
// which routes through the standard `error_types:` policy chain via
// applyErrorPolicy (default give_up when the template declares no
// `error_types: { park_timeout: ... }` override).

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// ParkedSweepArgs bundles the dependencies for SweepParkedNodes.
//
// ClaimHandles, ClaimHolders, AdvisoryLocker, and StoreRegistry are
// optional: when nil, the park_timeout watchdog still removes the
// node-run row but cannot fire auto-terminal Abandon on held
// claims associated with the dispatch. Wiring all four enables the
// blessed-invariant 13 path described in failOverdueParkedRow.
type ParkedSweepArgs struct {
	Persist        persistence.Tables
	Queue          persistence.Queue
	Clock          shared.Clock
	Logger         shared.Logger
	SupervisorID   string
	ClaimHandles   persistence.ClaimHandleTable
	AdvisoryLocker persistence.AdvisoryLocker
	StoreRegistry  *locks.Registry
	// Limit caps the per-tick batch. Zero falls back to 100.
	Limit int
	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook (plan I3). Threaded into the deadline-elapsed wake invalidate
	// so `rimsky_invalidates_total` covers parked-resume sweeps. Nil →
	// no-op.
	Metrics MetricsHook
	// PerReasonMaxPark holds the deployment-level per-reason cap. Keys
	// are the stored ParkReason values ("await_callback" / "snooze" —
	// the closed two-value enum); sweepParkedByReason does exact-equality
	// against the stored reason, so a key outside that set never matches a
	// parked row. The per-row col:rimsky_node_runs.max_park_duration_seconds
	// always takes priority — when set, it overrides any per-reason cap.
	// When the per-row column is NULL and the row's parked_reason matches
	// a key here, the per-reason cap applies. Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Parked-state taxonomy. Recommended defaults:
	//   await_callback: 7d, snooze: 1h.
	// Empty / nil → no per-reason cap is applied (only per-row caps fire).
	PerReasonMaxPark map[string]time.Duration
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

	// @constraint: max-park-duration overrun emits an Error terminal via
	// the standard error_types pipeline; the watchdog forces parked → failed
	// through the state machine's ReasonParkTimeout so default-policy
	// give_up applies when the template declares no `park_timeout` override.
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

	// @constraint: for rows whose per-row col:rimsky_node_runs.max_park_duration_seconds
	// is NULL, apply the deployment-level per-reason cap when a matching entry
	// exists in args.PerReasonMaxPark. Fallback order is per-row cap → per-reason
	// deployment cap → no cap; timeout produces failed{error_class: "park_timeout"}.
	if len(args.PerReasonMaxPark) > 0 {
		if err := sweepParkedByReason(ctx, args, now, log); err != nil {
			log.Warn("SweepParkedNodes: per-reason sweep failed", "error", err.Error())
		}
	}
	return nil
}

// sweepParkedByReason applies the deployment-level per-reason caps. It
// loads parked rows for each configured reason via ListParkedDiagnostic
// (which already filters by reason), then re-queries the per-row cap via
// GetParkedByNode to skip rows where the per-row column is set (those
// were handled by ListParkedOverdue in step 2). For rows without a
// per-row cap, parked_at + per-reason-cap < now triggers the standard
// fail-overdue pipeline.
//
// The two-query pass (ListParkedDiagnostic to find candidates;
// GetParkedByNode to load the full ParkedRow) is intentional: it keeps
// the persistence layer's read-projection split (the diagnostic
// projection is index-friendly but lacks the full payload columns the
// fail path needs).
func sweepParkedByReason(ctx context.Context, args ParkedSweepArgs, now time.Time, log shared.Logger) error {
	for reason, cap := range args.PerReasonMaxPark {
		if cap <= 0 {
			continue
		}
		diagnostic, err := listParkedForReason(ctx, args, reason)
		if err != nil {
			log.Warn("SweepParkedNodes: ListParkedDiagnostic per-reason failed",
				"reason", reason, "error", err.Error())
			continue
		}
		// @constraint: per-row col:rimsky_node_runs.max_park_duration_seconds takes priority over the deployment-level per-reason cap — rows with a per-row column are owned by ListParkedOverdue's dedicated path and skipped here to avoid double-waking. The per-reason sweep handles only rows where the per-row column is NULL.
		for _, d := range diagnostic {
			nodeID, err := uuid.Parse(d.NodeID)
			if err != nil {
				log.Warn("SweepParkedNodes: parse node_id", "node_id", d.NodeID, "error", err.Error())
				continue
			}
			// @constraint: the diagnostic projection lacks run_scope_id but
			// GetParkedByNode keys on (node_id, run_scope_id); look up the
			// run-tree row by DispatchID to resolve the RunScope first.
			var runScopeID shared.UUID
			if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				rt, err := args.Persist.RunTree().GetByID(ctx, tx, d.DispatchID)
				if err != nil || rt == nil {
					return err
				}
				runScopeID = rt.RunScopeID
				return nil
			}); err != nil || runScopeID == (shared.UUID{}) {
				continue
			}
			row, err := args.Queue.GetParkedByNode(ctx, nodeID, runScopeID)
			if err != nil || row == nil {
				continue
			}
			// @constraint: rows with MaxParkDurationSeconds set are
			// covered by ListParkedOverdue's dedicated path; this
			// cap-based sweep must skip them to avoid double-waking.
			if row.MaxParkDurationSeconds != nil {
				continue
			}
			deadline := row.ParkedAt.Add(cap)
			if deadline.After(now) {
				continue
			}
			// @constraint: race-guard mirroring ListParkedOverdue's filter —
			// when resume_at has already elapsed, the deadline-elapsed wake
			// path owns the row and the per-reason sweep must defer.
			if row.ResumeAt != nil && !row.ResumeAt.After(now) {
				continue
			}
			if err := failOverdueParkedRow(ctx, args, *row, log); err != nil {
				log.Warn("SweepParkedNodes: fail overdue (per-reason) failed",
					"node_id", row.NodeID.String(), "reason", reason, "error", err.Error())
			}
		}
	}
	return nil
}

// listParkedForReason wraps the diagnostic-list helper that needs a
// transaction. Opens a read-only tx for the duration of the listing.
func listParkedForReason(ctx context.Context, args ParkedSweepArgs, reason string) ([]persistence.ParkedDiagnosticRow, error) {
	var out []persistence.ParkedDiagnosticRow
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.Queue.ListParkedDiagnostic(ctx, tx, reason)
		if err != nil {
			return err
		}
		out = rows
		return nil
	})
	return out, err
}

// failOverdueParkedRow forces a parked node to failed with
// error_class="park_timeout". Before deleting the node-run row it
// drives the held-claim Abandon path (blessed invariant 13): each
// rimsky_claim_handles row anchored on this node has its claim-holders
// rows marked 'failed', then ResolveClaimHandleTerminal fires Abandon
// on the producer. Without this, held claims would survive past the
// node's failure indefinitely (the orphan reaper would eventually
// delete the rimsky_claim_handles row, but no Abandon verb would ever
// fire — leaking producer-side state).
func failOverdueParkedRow(ctx context.Context, args ParkedSweepArgs, row persistence.ParkedRow, log shared.Logger) error {
	target, err := loadTargetNode(ctx, args.Persist, row.NodeID)
	if err != nil || target == nil {
		return err
	}
	// @constraint: queue removal, state transition, held-claim Abandon, and
	// audit-log must be atomic — run them inside a single Persist.Transaction.
	// ParkedRow doesn't project run_scope_id, so resolve it from the run-tree
	// row first; state-machine writes key on (node, run_scope).
	var runScopeID shared.UUID
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rt, err := args.Persist.RunTree().GetByID(ctx, tx, row.DispatchID)
		if err != nil || rt == nil {
			return err
		}
		runScopeID = rt.RunScopeID
		return nil
	}); err != nil {
		return err
	}
	if runScopeID == (shared.UUID{}) {
		return fmt.Errorf("failOverdueParkedRow: no run scope for parked run %s", row.DispatchID)
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		parkTimeoutSig := "terminal/error/park_timeout"
		if err := args.Persist.Nodes().UpdateState(ctx, row.NodeID, runScopeID,
			cascade.NodeStateFailed, cascade.ReasonParkTimeout, &parkTimeoutSig, tx); err != nil {
			return err
		}
		// @concept: wait-set — parked → failed is a transition between
		// settled states, but any wait-set rows that landed between
		// park-time and timeout (e.g. via a concurrent cascade walk) must
		// release per the invariant "Bulk-delete on sender resolution
		// covers every topic kind uniformly." Post-stage-5 the wait-set
		// keys on sender_run_id; the parked row's DispatchID is the run id.
		if err := args.Persist.WaitSet().MarkDrainedBySender(ctx, row.FrameID, row.DispatchID, tx); err != nil {
			return err
		}
		// @constraint: mark held claim-holders rows as 'failed' so
		// CheckAndFireResolution computes the aggregate-failed → Abandon
		// outcome for every claim anchored on this node. Skipped when the
		// wire-time wiring isn't complete (ClaimHandles/StoreRegistry nil
		// — typical of unit tests).
		if args.ClaimHandles != nil && args.StoreRegistry != nil {
			if err := abandonHeldClaimsForOverdueNode(ctx, args, tx, row.NodeID, log); err != nil {
				return err
			}
		}
		// @constraint: thread runScopeID so fan-out children's retirement
		// lands on this specific run, not every sibling sharing the
		// node_id. Held claims are already resolved above so no
		// producer-side state is left dangling by the outright removal.
		if err := args.Queue.RemoveForNodeInTx(ctx, row.NodeID, runScopeID, "", tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &row.NodeID, InstanceID: &target.InstanceID,
			Kind: events.KindParkTimeout(),
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
			// @constraint: non-held claims lack claim-holders rows; their
			// terminal-decision is the active-terminal path on the owning
			// supervisor's runner, and the orphan reaper handles cleanup
			// since the owning node-run is going away.
			continue
		}
		if h.HolderSupervisorID == nil {
			// @constraint: non-active claim_handle (state ∈ {committed,
			// abandoned}) — auto-terminal already resolved this row.
			// FailAllActive and the claimant-guarded predicate would fail
			// with an empty supervisor id; skip silently because
			// CheckAndFireResolution is idempotent.
			continue
		}
		holderSupervisorID := *h.HolderSupervisorID
		if err := args.Persist.ClaimHolders().FailAllActiveByClaimHandle(ctx, h.ID, holderSupervisorID, tx); err != nil {
			return err
		}
		// @constraint: per the claimant-guarded-release invariant, the
		// resolution engine compares HolderSupervisorID against
		// runArgs.SupervisorID, which here is the scheduler's id
		// (different process). Override runArgs.SupervisorID to the row's
		// HolderSupervisorID for the duration of this resolution so the
		// claimant guard passes; the scheduler is an authorized observer
		// fulfilling cleanup the original (indefinitely-parked) holder
		// will never perform.
		runArgs.SupervisorID = holderSupervisorID
		if err := CheckAndFireResolution(ctx, runArgs, tx, h.ID); err != nil {
			return err
		}
	}
	return nil
}
