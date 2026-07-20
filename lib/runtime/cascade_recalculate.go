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
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
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

	sourceStr := "(external)"
	if args.SourceNodeID != nil {
		sourceStr = args.SourceNodeID.String()
	}

	var target *persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		t, err := sb.Nodes().Get(ctx, args.TargetNodeID, tx)
		if err != nil {
			return err
		}
		target = t
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

	if target.Executor == "" {
		return nil
	}

	var requiredClaimProducers []string
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := sb.Instances().Get(ctx, target.InstanceID, tx)
		if err != nil {
			return err
		}
		if inst == nil {
			return nil
		}
		tmpl, err := sb.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		if err != nil {
			return err
		}
		if tmpl == nil {
			return nil
		}
		if def := LookupNodeDef(&tmpl.Spec, target.NodeType); def != nil {
			requiredClaimProducers = node.RequiredClaimProducers(*def)
		}
		return nil
	}); err != nil {
		return err
	}
	if requiredClaimProducers == nil {
		requiredClaimProducers = []string{}
	}

	var runScopeID shared.UUID
	// @concept: executor
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := sb.Nodes().GetLatestRunForNode(ctx, tx, args.TargetNodeID)
		if err != nil {
			return err
		}
		if latest == nil || latest.State != cascade.NodeStateStale {
			return nil
		}
		runningFrID, err := sb.Frames().GetRunningFrameID(ctx, target.InstanceID, tx)
		if err != nil {
			return err
		}
		if runningFrID == nil || latest.FrameID != *runningFrID {
			return nil
		}
		runScopeID = latest.RunScopeID

		var priorNodeRunID *shared.UUID
		inFlightTarget := false
		inFlightID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, target.ID, runScopeID)
		if err != nil {
			return err
		}
		if ok {
			idCopy := inFlightID
			priorNodeRunID = &idCopy
			inFlightTarget = true

			rows, err := sb.WaitSet().ListForReceiver(ctx, *runningFrID, inFlightID, tx)
			if err != nil {
				return err
			}
			pending := 0
			for _, r := range rows {
				if r.DrainedAt == nil {
					pending++
				}
			}
			if pending > 0 {
				return nil
			}
		}
		if priorNodeRunID == nil {
			recentID, ok, err := args.Queue.GetMostRecentRunForNodeInScope(ctx, tx, target.ID, runScopeID)
			if err != nil {
				return fmt.Errorf("recalculate prior lookup: %w", err)
			}
			if ok && recentID != (shared.UUID{}) {
				idCopy := recentID
				priorNodeRunID = &idCopy
			}
		}
		var scratchInline []byte
		var scratchHandle, scratchBackend string
		if !inFlightTarget && priorNodeRunID != nil && *priorNodeRunID != (shared.UUID{}) {
			var lerr error
			scratchInline, scratchHandle, scratchBackend, lerr = args.Queue.LoadScratchInTx(ctx, tx, *priorNodeRunID)
			if lerr != nil {
				return fmt.Errorf("load prior scratch: %w", lerr)
			}
		}
		return args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                      target.ID,
			ExecutorName:                target.Executor,
			RequiredClaimProducers:      requiredClaimProducers,
			EnqueuedAt:                  args.Clock.Now(),
			FrameID:                     *runningFrID,
			RunScopeID:                  runScopeID,
			PriorNodeRunID:              priorNodeRunID,
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
