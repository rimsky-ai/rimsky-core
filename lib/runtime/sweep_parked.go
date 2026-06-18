// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

type ParkedSweepArgs struct {
	Persist          persistence.Tables
	Queue            persistence.Queue
	Clock            shared.Clock
	Logger           shared.Logger
	SupervisorID     string
	ClaimHandles     persistence.ClaimHandleTable
	AdvisoryLocker   persistence.AdvisoryLocker
	StoreRegistry    *locks.Registry
	Limit            int
	Metrics          MetricsHook
	PerReasonMaxPark map[string]time.Duration
}

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
		wakeArgs := WakeParkedArgs{
			Persist:      args.Persist,
			Queue:        args.Queue,
			Logger:       log,
			TargetNodeID: row.NodeID,
			SupervisorID: args.SupervisorID,
		}
		if args.Metrics != nil {
			args.Metrics.IncInvalidate("scheduler")
		}
		if err := WakeParkedNode(ctx, wakeArgs, WakeDeadlineElapsed); err != nil {
			log.Warn("SweepParkedNodes: wake failed",
				"node_id", row.NodeID.String(), "error", err.Error())
		}
	}

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

	if len(args.PerReasonMaxPark) > 0 {
		if err := sweepParkedByReason(ctx, args, now, log); err != nil {
			log.Warn("SweepParkedNodes: per-reason sweep failed", "error", err.Error())
		}
	}
	return nil
}

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
		for _, d := range diagnostic {
			nodeID, err := uuid.Parse(d.NodeID)
			if err != nil {
				log.Warn("SweepParkedNodes: parse node_id", "node_id", d.NodeID, "error", err.Error())
				continue
			}
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
			if row.MaxParkDurationSeconds != nil {
				continue
			}
			deadline := row.ParkedAt.Add(cap)
			if deadline.After(now) {
				continue
			}
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

func failOverdueParkedRow(ctx context.Context, args ParkedSweepArgs, row persistence.ParkedRow, log shared.Logger) error {
	target, err := loadTargetNode(ctx, args.Persist, row.NodeID)
	if err != nil || target == nil {
		return err
	}
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
		if args.ClaimHandles != nil && args.StoreRegistry != nil {
			if err := abandonHeldClaimsForOverdueNode(ctx, args, tx, row.NodeID, log); err != nil {
				return err
			}
		}
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
			continue
		}
		if h.HolderSupervisorID == nil {
			continue
		}
		holderSupervisorID := *h.HolderSupervisorID
		if err := args.Persist.ClaimHolders().FailAllActiveByClaimHandle(ctx, h.ID, holderSupervisorID, tx); err != nil {
			return err
		}
		runArgs.SupervisorID = holderSupervisorID
		if err := CheckAndFireResolution(ctx, runArgs, tx, h.ID); err != nil {
			return err
		}
	}
	return nil
}
