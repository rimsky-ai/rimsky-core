// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scheduler

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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

	var ready []persistence.PureCascadeReadyRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := sb.Nodes().ListPureCascadeReady(ctx, tx)
		ready = rows
		return err
	}); err != nil {
		return 0, err
	}

	count := 0
	for _, n := range ready {
		def := lookupTemplateNodeDefByType(ctx, sb, n.InstanceID, n.NodeType)
		if acquiresClaims(def) {
			prepared, err := prepareNativeClaimRouting(ctx, args, n, def)
			if err != nil {
				log.Warn("ProcessPureCascade: prepare native claim routing failed",
					"node_id", n.NodeID.String(), "error", err.Error())
				continue
			}
			if prepared {
				count++
			}
			continue
		}
		if err := transitionPureCascade(ctx, args, n, log); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

func transitionPureCascade(ctx context.Context, args PureCascadeArgs, n persistence.PureCascadeReadyRow, log shared.Logger) error {
	sb := args.Persist
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pureCascadeSig := "terminal/success"
		if err := sb.Nodes().UpdateState(ctx, n.NodeRunID, cascade.NodeStateFresh, cascade.ReasonPureCascade, &pureCascadeSig, tx); err != nil {
			return err
		}
		if err := args.Queue.RemoveForNodeInTx(ctx, n.NodeID, n.RunScopeID, "", tx); err != nil {
			return err
		}
		runArgs := runtime.RunArgs{Persist: sb, Queue: args.Queue, Clock: args.Clock, Logger: log}
		return runtime.EmitTerminalSuccessAndDrainInTx(ctx, runArgs, tx,
			n.NodeID, n.NodeType, n.NodeRunID, n.InstanceID, n.FrameID, "pure_cascade")
	}); err != nil {
		log.Warn("ProcessPureCascade: state transition + cascade failed",
			"node_id", n.NodeID.String(), "error", err.Error())
		return err
	}
	return nil
}

func prepareNativeClaimRouting(ctx context.Context, args PureCascadeArgs, n persistence.PureCascadeReadyRow, def *nodepkg.TemplateNodeDef) (bool, error) {
	required := nodepkg.RequiredClaimProducers(*def)
	if required == nil {
		required = []string{}
	}
	var prepared bool
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := args.Persist.Nodes().SetRunRequiredStores(ctx, tx, n.NodeRunID, required)
		prepared = ok
		return err
	})
	return prepared, err
}

func lookupTemplateNodeDefByType(ctx context.Context, sb persistence.Tables, instanceID shared.UUID, nodeType string) *nodepkg.TemplateNodeDef {
	var inst *persistence.InstanceRow
	var tmpl *persistence.TemplateRow
	_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := sb.Instances().Get(ctx, instanceID, tx)
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
		if tmpl.Spec.Nodes[i].Type == nodeType {
			return &tmpl.Spec.Nodes[i]
		}
	}
	return nil
}

func acquiresClaims(def *nodepkg.TemplateNodeDef) bool {
	if def == nil {
		return false
	}
	return len(def.ClaimProducers) > 0
}
