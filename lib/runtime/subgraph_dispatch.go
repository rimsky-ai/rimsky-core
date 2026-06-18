// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//	@concept: sub-graph
//	@concept: delegation
//	@concept: run-scope
//     `@blessed-invariant: exit-node-writeback` annotation at the carry

package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type SubgraphInternalCascadeArgs struct {
	CallingNodeRunID shared.UUID
	CallingNodeID shared.UUID
	InstanceID shared.UUID
	FrameID shared.UUID
	Template *node.TemplateSpec
	DelegateGraphName string
}

func SubgraphInternalCascade(in SubgraphInternalCascadeArgs) ([]node.TemplateNodeDef, error) {
	if in.Template == nil {
		return nil, fmt.Errorf("SubgraphInternalCascade: Template is required")
	}
	if in.DelegateGraphName == "" {
		return nil, fmt.Errorf("SubgraphInternalCascade: DelegateGraphName is required")
	}
	for _, g := range in.Template.Graphs {
		if g.Name != in.DelegateGraphName {
			continue
		}
		out := make([]node.TemplateNodeDef, 0, len(g.Nodes))
		for _, n := range g.Nodes {
			if n.Type == g.Entry {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
	return nil, fmt.Errorf(
		"SubgraphInternalCascade: delegate graph %q not declared in template",
		in.DelegateGraphName)
}

func SubgraphParentSuccessCascade(
	in SubgraphInternalCascadeArgs,
) (internalNodes []node.TemplateNodeDef, transitionReason cascade.TransitionReason, err error) {
	internals, err := SubgraphInternalCascade(in)
	if err != nil {
		return nil, cascade.TransitionReason{}, err
	}
	if _, err := cascade.NextStateParent(cascade.NodeStateRunning, cascade.ReasonSubGraphInternalCascadeFired); err != nil {
		if !cascade.IsParentAggregateOK(err) {
			return nil, cascade.TransitionReason{}, fmt.Errorf(
				"SubgraphParentSuccessCascade: state-machine rejects running→running under subgraph_internal_cascade_fired: %w", err)
		}
	}
	return internals, cascade.ReasonSubGraphInternalCascadeFired, nil
}

func IsSubgraphCaller(def *node.TemplateNodeDef) bool {
	if def == nil {
		return false
	}
	return def.Delegate != ""
}

func IsSubgraphExit(tmpl *node.TemplateSpec, nodeType string) bool {
	if tmpl == nil {
		return false
	}
	for _, g := range tmpl.Graphs {
		if g.Name == spec.MainGraphName {
			continue
		}
		if g.Exit == nodeType {
			return true
		}
	}
	return false
}

//	@concept: sub-graph
//	@concept: run-scope
func applyTerminalCompleteSubgraphCaller(
	ctx context.Context, args RunArgs, acq *acquisition,
	merged map[string]any, t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	settlingSig := "terminal/success"

	if _, err := cascade.NextStateParent(cascade.NodeStateRunning, cascade.ReasonSubGraphInternalCascadeFired); err != nil {
		if !cascade.IsParentAggregateOK(err) {
			return nil, fmt.Errorf(
				"applyTerminalCompleteSubgraphCaller: state-machine rejects running→running under subgraph_internal_cascade_fired: %w",
				err)
		}
	}

	var internalNodes []node.TemplateNodeDef
	var tmplSpec *node.TemplateSpec
	{
		inst, err := args.Persist.Instances().Get(ctx, acq.InstanceID, tx)
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: load instance: %w", err)
		}
		if inst != nil {
			tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil {
				return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: load template: %w", err)
			}
			if tmpl != nil {
				tmplSpec = &tmpl.Spec
			}
		}
	}
	if tmplSpec != nil {
		nodes, err := SubgraphInternalCascade(SubgraphInternalCascadeArgs{
			CallingNodeRunID:  acq.DispatchID,
			CallingNodeID:     acq.NodeID,
			InstanceID:        acq.InstanceID,
			FrameID:           acq.FrameID,
			Template:          tmplSpec,
			DelegateGraphName: acq.NodeDef.Delegate,
		})
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: resolve internals: %w", err)
		}
		internalNodes = nodes
	}

	if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: upsert attributes: %w", err)
	}
	// @concept: executor
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, t.Scratch); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: %w", err)
	}
	if err := args.Persist.RunTree().UpdateStateAndOutcome(ctx, tx, acq.DispatchID,
		cascade.NodeStateRunning, &settlingSig); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: update run-tree: %w", err)
	}
	if len(internalNodes) > 0 {
		instNodes, err := args.Persist.Nodes().ListByInstance(ctx, acq.InstanceID, tx)
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: list instance nodes: %w", err)
		}
		byType := make(map[string]persistence.NodeRow, len(instNodes))
		for _, n := range instNodes {
			byType[n.NodeType] = n
		}
		children := make([]ChildRunSpec, 0, len(internalNodes))
		for _, def := range internalNodes {
			nrow, ok := byType[def.Type]
			if !ok {
				return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: internal node %q has no rimsky_nodes row in instance %s",
					def.Type, acq.InstanceID.String())
			}
			children = append(children, ChildRunSpec{
				NodeID:         nrow.ID,
				Executor:       def.Executor,
				RequiredStores: node.RequiredStores(def),
			})
		}
		if _, err := DispatchChildren(ctx, args, tx, ChildExecutionInput{
			ParentRunID:       acq.DispatchID,
			ParentRunScopeID:  acq.RunScopeID,
			InstanceID:        acq.InstanceID,
			FrameID:           acq.FrameID,
			ChildGraphName:    acq.NodeDef.Delegate,
			AggregationPolicy: spec.AggregationPolicy{},
			EntryAbsorbed:     true,
			Partitions:        []PartitionDescriptor{{PartitionKey: ""}},
			Children:          children,
		}); err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: %w", err)
		}
	}
	childAliases := make([]string, 0, len(internalNodes))
	for _, def := range internalNodes {
		childAliases = append(childAliases, def.Type)
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindSubgraphInternalCascadeFired(),
		Payload: map[string]any{
			"delegate_graph":       acq.NodeDef.Delegate,
			"calling_run_id":       acq.DispatchID.String(),
			"settling_signal_type": settlingSig,
			"changed":              t.Changed,
			"transition_reason":    cascade.ReasonSubGraphInternalCascadeFired.Kind,
			"child_count":          len(internalNodes),
		},
	}, tx); err != nil {
		return nil, err
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindSubgraphDispatched(),
		Payload: map[string]any{
			"caller_run_id":  acq.DispatchID.String(),
			"caller_node_id": acq.NodeID.String(),
			"subgraph_name":  acq.NodeDef.Delegate,
			"child_aliases":  childAliases,
			"child_count":    len(internalNodes),
		},
	}, tx); err != nil {
		return nil, err
	}

	if args.Logger != nil {
		args.Logger.Info("subgraph: parent run staying running for internal cascade",
			"calling_run_id", acq.DispatchID.String(),
			"node_type", acq.NodeType,
			"delegate", acq.NodeDef.Delegate,
			"settling_signal_type", settlingSig)
	}
	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			RunID:              dispatchID,
			NodeID:             acq.NodeID,
			State:              string(cascade.NodeStateRunning),
			SettlingSignalType: settlingSig,
			Changed:            t.Changed,
			TerminalKind:       "subgraph_call",
			NodeAlias:          acq.NodeType,
			ExecutorName:       acq.Executor,
			TemplateHash:       acq.TemplateHash,
			Params:             acq.InstanceParams,
			AttributesMerged:   acq.MergedAttributes,
			HeldClaims:         HeldClaimsForLineage(acq),
			ParentRunID:        scope.ParentRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
	}
	return post, nil
}

//	@concept: sub-graph
//	@concept: delegation
//	@concept: run-scope
func applyTerminalCompleteSubgraphExit(
	ctx context.Context, args RunArgs, acq *acquisition,
	merged map[string]any, tx persistence.Tx,
) error {
	var wb []byte
	if len(merged) > 0 {
		encoded, err := json.Marshal(merged)
		if err != nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphExit: encode writeback: %w", err)
		}
		wb = encoded
	}
	return SettleChildren(ctx, args, tx, ChildSettlementInput{
		Policy:        spec.AggregationPolicy{Kind: spec.AggregationKindCarryVerbatim},
		ExitRunID:     acq.DispatchID,
		ExitNodeID:    acq.NodeID,
		ExitNodeAlias: acq.NodeType,
		InstanceID:    acq.InstanceID,
		Writeback:     wb,
	})
}

func isSubgraphExitNode(acq *acquisition) bool {
	if acq == nil || acq.NodeDef == nil {
		return false
	}
	return acq.NodeDef.IsSubgraphExit
}
