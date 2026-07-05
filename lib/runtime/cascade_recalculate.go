// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: cascade
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

type RecalculateArgs struct {
	Persist      persistence.Tables
	Queue        persistence.Queue
	Clock        shared.Clock
	Logger       shared.Logger
	SourceNodeID *shared.UUID
	TargetNodeID shared.UUID
}

func RecalculateNode(ctx context.Context, args RecalculateArgs) error {
	sb, log := args.Persist, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	_ = log

	sourceStr := "(external)"
	if args.SourceNodeID != nil {
		sourceStr = args.SourceNodeID.String()
	}

	var (
		target      *persistence.NodeRow
		latest      *persistence.NodeRunLatest
		runningFrID *shared.UUID
	)
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		t, err := sb.Nodes().Get(ctx, args.TargetNodeID, tx)
		if err != nil {
			return err
		}
		target = t
		if t == nil {
			return nil
		}
		l, err := sb.Nodes().GetLatestRunForNode(ctx, tx, args.TargetNodeID)
		if err != nil {
			return err
		}
		latest = l
		fr, err := sb.Frames().GetRunningFrameID(ctx, t.InstanceID, tx)
		if err != nil {
			return err
		}
		runningFrID = fr
		return nil
	}); err != nil {
		return err
	}
	if target == nil {
		return nil
	}

	_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return sb.Events().Append(ctx, persistence.EventAppendInput{
			InstanceID: &target.InstanceID,
			NodeID:     &args.TargetNodeID,
			Kind:       events.KindMessageReceived(),
			Payload: map[string]any{
				"type":           "recalculate",
				"source_node_id": sourceStr,
				"target_node_id": args.TargetNodeID.String(),
			},
		}, tx)
	})
	if latest == nil || latest.State != cascade.NodeStateStale {
		return nil
	}
	if runningFrID == nil {
		return nil
	}
	if latest.FrameID != *runningFrID {
		return nil
	}
	runScopeID := latest.RunScopeID
	var pending int
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, target.ID, runScopeID)
		if err != nil {
			return err
		}
		if !ok {
			pending = 0
			return nil
		}
		rows, err := sb.WaitSet().ListForReceiver(ctx, *runningFrID, runID, tx)
		if err != nil {
			return err
		}
		pending = len(rows)
		return nil
	}); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}

	if target.Executor == "" {
		return nil
	}
	// @concept: executor
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var priorDispatchID *shared.UUID
		inFlightTarget := false
		if inFlightID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, target.ID, runScopeID); err != nil {
			return err
		} else if ok {
			idCopy := inFlightID
			priorDispatchID = &idCopy
			inFlightTarget = true
		}
		if priorDispatchID == nil {
			recentID, ok, err := args.Queue.GetMostRecentRunForNodeInScope(ctx, tx, target.ID, runScopeID)
			if err != nil {
				return fmt.Errorf("recalculate prior lookup: %w", err)
			}
			if ok && recentID != (shared.UUID{}) {
				idCopy := recentID
				priorDispatchID = &idCopy
			}
		}
		var scratchInline []byte
		var scratchHandle, scratchBackend string
		if !inFlightTarget && priorDispatchID != nil && *priorDispatchID != (shared.UUID{}) {
			var lerr error
			scratchInline, scratchHandle, scratchBackend, lerr = args.Queue.LoadScratchInTx(ctx, tx, *priorDispatchID)
			if lerr != nil {
				return fmt.Errorf("load prior scratch: %w", lerr)
			}
		}
		return args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                      target.ID,
			ExecutorName:                target.Executor,
			RequiredClaimProducers:      []string{},
			EnqueuedAt:                  args.Clock.Now(),
			FrameID:                     *runningFrID,
			RunScopeID:                  runScopeID,
			PriorDispatchID:             priorDispatchID,
			PriorDispatchDisposition:    "recalculate",
			InitialScratchInline:        scratchInline,
			InitialScratchHandle:        scratchHandle,
			InitialScratchHandleBackend: scratchBackend,
			CreationReason:              cascade.CreationReasonRecalculate,
		}, tx)
	}); err != nil {
		if errors.Is(err, persistence.ErrRunScopeClosed) {
			log.Debug("RecalculateNode: skip enqueue: run scope closed",
				"node_id", target.ID.String(),
				"run_scope_id", runScopeID.String())
			return nil
		}
		return err
	}
	return nil
}
