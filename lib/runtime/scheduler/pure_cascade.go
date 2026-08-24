// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scheduler

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	graphsched "github.com/rimsky-ai/rimsky-core/lib/graph/scheduler"
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

	ready, err := graphsched.ListPureCascadeReady(ctx, sb)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, n := range ready {
		def, err := lookupTemplateNodeDefByType(ctx, sb, n.InstanceID, n.NodeType)
		if err != nil {
			log.Warn("CASCADE.TEMPLATENODEDEF.LOOKUPFAILED", "site", "ProcessPureCascade", "detail", "skipping this pass; the next tick retries",
				"node_id", n.NodeID.String(), "node_type", n.NodeType, "error", err.Error())
			continue
		}
		if graphsched.AcquiresClaims(def) {
			prepared, err := graphsched.PrepareNativeClaimRouting(ctx, sb, n, def)
			if err != nil {
				log.Warn("CASCADE.NATIVECLAIMROUTING.PREPAREFAILED", "site", "ProcessPureCascade",
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
		if err := runtime.AppendStateTransitionEvent(ctx, sb, n.NodeID, n.InstanceID,
			cascade.NodeStateStale, cascade.NodeStateFresh, cascade.ReasonPureCascade, tx); err != nil {
			return err
		}
		if err := args.Queue.ForceRemoveForNode(ctx, n.NodeID, n.RunScopeID, tx); err != nil {
			return err
		}
		return runtime.EmitTerminalSuccessAndDrainInTx(ctx, runArgs, n.NodeID, n.NodeType, n.NodeRunID, n.InstanceID, n.FrameID, "pure_cascade", tx)
	}); err != nil {
		log.Warn("CASCADE.STATETRANSITION.FAILED", "site", "ProcessPureCascade",
			"node_id", n.NodeID.String(), "error", err.Error())
		return err
	}
	if _, err := runtime.PropagateIfChildAfterTerminal(ctx, runArgs, n.NodeRunID); err != nil {
		log.Warn("RUNTREE.PROPAGATION.FAILED", "site", "ProcessPureCascade",
			"node_id", n.NodeID.String(), "error", err.Error())
	}
	evaluateAfterTerminalBreakpoint(ctx, runArgs, n, log)
	return nil
}

// @concept: breakpoint
func evaluateAfterTerminalBreakpoint(ctx context.Context, runArgs runtime.RunArgs, n persistence.PureCascadeReadyRow, log shared.Logger) {
	var scope persistence.RunScopeRow
	if err := runArgs.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rs, err := runArgs.Persist.RunScopes().GetByID(ctx, n.RunScopeID, tx)
		if err != nil {
			return err
		}
		if rs != nil {
			scope = *rs
		}
		return nil
	}); err != nil {
		log.Warn("CASCADE.BREAKPOINTRUNSCOPE.RESOLVEFAILED", "site", "ProcessPureCascade", "detail", "continuing",
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
		log.Warn("BREAKPOINT.AFTERTERMINALEVAL.FAILED", "site", "ProcessPureCascade", "detail", "continuing",
			"node_id", n.NodeID.String(), "error", err.Error())
	}
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
