// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scheduler

import (
	"context"
	"errors"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

type PureCascadeArgs struct {
	Persist persistence.Tables
	Queue   persistence.Queue
	Clock   shared.Clock
	Logger  shared.Logger
}

func ProcessPureCascade(ctx context.Context, args PureCascadeArgs) (int, error) {
	sb := args.Persist
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	var ready []persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := sb.Nodes().ListPureCascadeReady(ctx, tx)
		ready = rows
		return err
	}); err != nil {
		return 0, err
	}

	count := 0
	for _, n := range ready {
		def := lookupTemplateNodeDef(ctx, sb, n)
		// @story: cascade-emit
		// @concept: message-emitter-node
		if hasClaimStore(def) || isEmitMessage(def) {
			if err := enqueueNativeClaimOnly(ctx, args, n, def); err != nil {
				if errors.Is(err, persistence.ErrRunScopeClosed) {
					log.Debug("ProcessPureCascade: skip native claim-only enqueue: run scope closed",
						"node_id", n.ID.String())
					continue
				}
				log.Warn("ProcessPureCascade: enqueue native claim-only failed",
					"node_id", n.ID.String(), "error", err.Error())
				continue
			}
			count++
			continue
		}
		if err := transitionPureCascade(ctx, args, n, log); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

func transitionPureCascade(ctx context.Context, args PureCascadeArgs, n persistence.NodeRow, log shared.Logger) error {
	sb := args.Persist
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if n.RunScopeID == nil {
			return nil
		}
		pureCascadeSig := "terminal/success"
		if err := sb.Nodes().UpdateState(ctx, n.ID, *n.RunScopeID, cascade.NodeStateFresh, cascade.ReasonPureCascade, &pureCascadeSig, tx); err != nil {
			return err
		}
		return args.Queue.RemoveForNodeInTx(ctx, n.ID, *n.RunScopeID, "", tx)
	}); err != nil {
		log.Warn("ProcessPureCascade: state transition failed",
			"node_id", n.ID.String(), "error", err.Error())
		return err
	}
	nodeID := n.ID
	instanceID := n.InstanceID
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		successSig := signalpkg.Signal{
			Type: signalpkg.TypePath("terminal/success"),
			Payload: map[string]any{
				"changed":          false,
				"attributes_delta": map[string]any{},
				"change_summary":   "pure_cascade",
			},
		}
		return signalaudit.EmitSignal(ctx, sb.Events(), instanceID, nodeID, successSig, args.Clock.Now(), tx)
	}); err != nil {
		log.Warn("ProcessPureCascade: emit terminal/success signal failed",
			"node_id", n.ID.String(), "error", err.Error())
	}
	var receivers []persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := sb.Instances().Get(ctx, n.InstanceID, tx)
		if err != nil || inst == nil {
			return err
		}
		row, err := sb.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		if err != nil || row == nil {
			return err
		}
		subs := nodepkg.ExtractSubstitutionRefsFromTemplate(row.Spec)
		msgs := nodepkg.ExtractMessageRefsFromTemplate(row.Spec)
		edges, err := nodepkg.BuildSubscriptionEdges(row.Spec, subs, msgs)
		if err != nil {
			return err
		}
		if edges == nil {
			return nil
		}
		// @concept: cascade
		// @decision: wake-on-change-wait-set-only
		allEdges := edges.ReceiverEdgesForSender(n.NodeType)
		if len(allEdges) == 0 {
			return nil
		}
		want := make(map[string]struct{})
		for _, e := range allEdges {
			if !e.WakeOnChange {
				continue
			}
			want[e.ReceiverNodeType] = struct{}{}
		}
		if len(want) == 0 {
			return nil
		}
		instNodes, err := sb.Nodes().ListByInstance(ctx, n.InstanceID, tx)
		if err != nil {
			return err
		}
		for _, x := range instNodes {
			if x.ID == n.ID {
				continue
			}
			if _, ok := want[x.NodeType]; ok {
				receivers = append(receivers, x)
			}
		}
		return nil
	}); err != nil {
		log.Warn("ProcessPureCascade: list receivers failed",
			"node_id", n.ID.String(), "error", err.Error())
		return nil
	}
	var sourceRunScopeID shared.UUID
	if n.RunScopeID != nil {
		sourceRunScopeID = *n.RunScopeID
	}
	for _, dep := range receivers {
		if n.FrameID != nil && sourceRunScopeID != (shared.UUID{}) {
			affirmErr := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return sb.Nodes().AffirmNodeRunRow(ctx, dep.ID, sourceRunScopeID, *n.FrameID, tx)
			})
			if errors.Is(affirmErr, persistence.ErrRunScopeClosed) {
				continue
			}
			if affirmErr != nil {
				log.Warn("ProcessPureCascade: affirm receiver run row failed",
					"source_node_id", n.ID.String(),
					"target_node_id", dep.ID.String(),
					"error", affirmErr.Error())
			}
		}
		if n.FrameID != nil {
			cascadePropagateFrameID(ctx, sb, args.Queue, dep.ID, *n.FrameID, log)
		}
		srcID := n.ID
		if rerr := runtime.RecalculateNode(ctx, runtime.RecalculateArgs{
			Persist:      sb,
			Queue:        args.Queue,
			Clock:        args.Clock,
			Logger:       log,
			SourceNodeID: &srcID,
			TargetNodeID: dep.ID,
		}); rerr != nil {
			log.Warn("ProcessPureCascade: recalculate failed",
				"source_node_id", n.ID.String(),
				"target_node_id", dep.ID.String(),
				"error", rerr.Error())
		}
	}
	return nil
}

func enqueueNativeClaimOnly(ctx context.Context, args PureCascadeArgs, n persistence.NodeRow, def *nodepkg.TemplateNodeDef) error {
	required := nodepkg.RequiredStores(*def)
	if required == nil {
		required = []string{}
	}
	if n.FrameID == nil {
		return nil
	}
	if n.RunScopeID == nil {
		return nil
	}
	executorName := ""
	if isEmitMessage(def) {
		executorName = runtime.EmitMessageDispatchName
	}
	return args.Queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:         n.ID,
		ExecutorName:   executorName,
		RequiredStores: required,
		EnqueuedAt:     args.Clock.Now(),
		FrameID:        *n.FrameID,
		RunScopeID:     *n.RunScopeID,
	})
}

func lookupTemplateNodeDef(ctx context.Context, sb persistence.Tables, n persistence.NodeRow) *nodepkg.TemplateNodeDef {
	var inst *persistence.InstanceRow
	var tmpl *persistence.TemplateRow
	_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := sb.Instances().Get(ctx, n.InstanceID, tx)
		if err != nil || i == nil {
			return err
		}
		inst = i
		t, err := sb.Templates().GetByHash(ctx, i.TemplateHash, tx)
		tmpl = t
		return err
	})
	if inst == nil || tmpl == nil {
		return nil
	}
	for i := range tmpl.Spec.Nodes {
		if tmpl.Spec.Nodes[i].Type == n.NodeType {
			return &tmpl.Spec.Nodes[i]
		}
	}
	return nil
}

func hasClaimStore(def *nodepkg.TemplateNodeDef) bool {
	if def == nil {
		return false
	}
	return len(def.Stores) > 0
}

// @concept: message-emitter-node
func isEmitMessage(def *nodepkg.TemplateNodeDef) bool {
	if def == nil {
		return false
	}
	return def.EmitsMessage != ""
}

func cascadePropagateFrameID(ctx context.Context, sb persistence.Tables, queue persistence.Queue, childID shared.UUID, frameID shared.UUID, log shared.Logger) {
	err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		child, err := sb.Nodes().Get(ctx, childID, tx)
		if err != nil || child == nil {
			return err
		}
		if child.RunScopeID == nil {
			return nil
		}
		runID, ok, err := queue.GetInFlightRunForNode(ctx, tx, child.ID, *child.RunScopeID)
		if err != nil || !ok {
			return err
		}
		return sb.Nodes().MarkStaleForCascade(ctx, runID, frameID, tx)
	})
	if err != nil && log != nil {
		log.Warn("cascadePropagateFrameID: failed",
			"child_id", childID.String(),
			"frame_id", frameID.String(),
			"error", err.Error())
	}
}
