// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scheduler

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
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
	if args.Clock == nil {
		args.Clock = shared.SystemClock{}
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
		def, err := lookupTemplateNodeDefByType(ctx, sb, n.InstanceID, n.NodeType)
		if err != nil {
			log.Warn("ProcessPureCascade: lookup template node def failed; skipping this pass, will retry next tick",
				"node_id", n.NodeID.String(), "node_type", n.NodeType, "error", err.Error())
			continue
		}
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

// @concept: run-scope
func transitionPureCascade(ctx context.Context, args PureCascadeArgs, n persistence.PureCascadeReadyRow, log shared.Logger) error {
	sb := args.Persist
	runArgs := runtime.RunArgs{Persist: sb, Queue: args.Queue, Clock: args.Clock, Logger: log}
	pureCascadeSig := "terminal/success"
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Nodes().UpdateState(ctx, n.NodeRunID, cascade.NodeStateFresh, cascade.ReasonPureCascade, &pureCascadeSig, tx); err != nil {
			return err
		}
		if err := args.Queue.ForceRemoveForNodeInTx(ctx, n.NodeID, n.RunScopeID, tx); err != nil {
			return err
		}
		return runtime.EmitTerminalSuccessAndDrainInTx(ctx, runArgs, tx,
			n.NodeID, n.NodeType, n.NodeRunID, n.InstanceID, n.FrameID, "pure_cascade")
	}); err != nil {
		log.Warn("ProcessPureCascade: state transition + cascade failed",
			"node_id", n.NodeID.String(), "error", err.Error())
		return err
	}
	runtime.EmitLeafRunLineage(ctx, runArgs, runtime.LeafRunEmitInput{
		InstanceID:         n.InstanceID,
		FrameID:            n.FrameID,
		NodeRunID:          n.NodeRunID,
		NodeID:             n.NodeID,
		State:              string(cascade.NodeStateFresh),
		SettlingSignalType: pureCascadeSig,
		TerminalKind:       "pure_cascade",
		NodeAlias:          n.NodeType,
	})
	if _, err := runtime.PropagateIfChildAfterTerminal(ctx, runArgs, n.NodeRunID,
		cascade.NodeStateFresh, &pureCascadeSig); err != nil {
		log.Warn("ProcessPureCascade: run-tree propagation failed",
			"node_id", n.NodeID.String(), "error", err.Error())
	}
	evaluateAfterTerminalBreakpoint(ctx, runArgs, n, log)
	return nil
}

// @concept: breakpoint
func evaluateAfterTerminalBreakpoint(ctx context.Context, runArgs runtime.RunArgs, n persistence.PureCascadeReadyRow, log shared.Logger) {
	var scope persistence.RunScopeRow
	if err := runArgs.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rs, err := runArgs.Persist.RunScopes().GetByID(ctx, tx, n.RunScopeID)
		if err != nil {
			return err
		}
		if rs != nil {
			scope = *rs
		}
		return nil
	}); err != nil {
		log.Warn("ProcessPureCascade: resolve run scope for breakpoint eval failed; continuing",
			"node_id", n.NodeID.String(), "error", err.Error())
	}
	terminalSig := signalpkg.BuildTerminalSuccessSignal(false, nil, "", nil)
	if _, err := runtime.EvaluateBreakpoints(ctx, runArgs, runtime.CheckpointContext{
		InstanceID:     n.InstanceID,
		NodeID:         n.NodeID,
		NodeRunID:      n.NodeRunID,
		FrameID:        n.FrameID,
		NodeType:       n.NodeType,
		Graph:          scope.GraphName,
		ChildKey:       scope.PartitionKey,
		Checkpoint:     persistence.CheckpointAfterTerminal,
		TerminalSignal: &terminalSig,
	}); err != nil {
		log.Warn("ProcessPureCascade: breakpoint after_terminal eval failed; continuing",
			"node_id", n.NodeID.String(), "error", err.Error())
	}
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

func lookupTemplateNodeDefByType(ctx context.Context, sb persistence.Tables, instanceID shared.UUID, nodeType string) (*nodepkg.TemplateNodeDef, error) {
	var inst *persistence.InstanceRow
	var tmpl *persistence.TemplateRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := sb.Instances().Get(ctx, instanceID, tx)
		if err != nil || i == nil {
			return err
		}
		inst = i
		t, err := sb.Templates().GetByHash(ctx, i.TemplateHash, tx)
		tmpl = t
		return err
	}); err != nil {
		return nil, err
	}
	if inst == nil || tmpl == nil {
		return nil, nil
	}
	return runtime.LookupNodeDef(&tmpl.Spec, nodeType), nil
}

func acquiresClaims(def *nodepkg.TemplateNodeDef) bool {
	if def == nil {
		return false
	}
	return len(def.ClaimProducers) > 0
}
