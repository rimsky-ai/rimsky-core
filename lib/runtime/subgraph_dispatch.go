// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: sub-graph
// @concept: delegation
// @concept: run-scope

package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type SubgraphInternalCascadeArgs struct {
	Template          *node.TemplateSpec
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
) (internalNodes []node.TemplateNodeDef, err error) {
	return SubgraphInternalCascade(in)
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

// @concept: sub-graph
// @concept: run-scope
// @decision: fan-out-and-delegation-are-distinct-mechanisms
func applyTerminalCompleteSubgraphCaller(
	ctx context.Context, args RunArgs, acq *acquisition,
	merged map[string]any, t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	settlingSig := "terminal/success"

	var internalNodes []node.TemplateNodeDef
	var tmplSpec *node.TemplateSpec
	{
		inst, err := args.Persist.Instances().Get(ctx, acq.InstanceID, tx)
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: load instance: %w", err)
		}
		if inst == nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: instance %s not found", acq.InstanceID.String())
		}
		tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: load template: %w", err)
		}
		if tmpl == nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: template %q not found for instance %s",
				inst.TemplateHash, acq.InstanceID.String())
		}
		tmplSpec = &tmpl.Spec
	}
	{
		nodes, err := SubgraphInternalCascade(SubgraphInternalCascadeArgs{
			Template:          tmplSpec,
			DelegateGraphName: acq.NodeDef.Delegate,
		})
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: resolve internals: %w", err)
		}
		internalNodes = nodes
	}

	if err := upsertFinalAttributesTx(ctx, args, acq, merged, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: upsert attributes: %w", err)
	}
	// @concept: executor
	if err := applyTerminalScratchInTx(ctx, args, acq, t.Scratch, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: %w", err)
	}
	stayState, err := cascade.NextStateParent(cascade.NodeStateRunning, cascade.ReasonSubGraphInternalCascadeFired)
	if err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: audit running-stay transition: %w", err)
	}
	if err := args.Persist.NodeRunTree().UpdateStateAndOutcome(ctx, acq.NodeRunID, stayState, &settlingSig, t.Changed, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: update run-tree: %w", err)
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, nil, nil, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: emit attribute changes: %w", err)
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
				NodeID:                 nrow.ID,
				Executor:               def.Executor,
				RequiredClaimProducers: node.RequiredClaimProducers(def),
			})
		}
		dispatched, err := DispatchChildren(ctx, args, ChildExecutionInput{
			ParentNodeRunID:   acq.NodeRunID,
			ParentRunScopeID:  acq.RunScopeID,
			InstanceID:        acq.InstanceID,
			FrameID:           acq.FrameID,
			ChildGraphName:    acq.NodeDef.Delegate,
			AggregationPolicy: spec.AggregationPolicy{},
			EntryAbsorbed:     true,
			Partitions:        []PartitionDescriptor{{PartitionKey: ""}},
			Children:          children,
		}, tx)
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: %w", err)
		}
		typeByNodeID := make(map[shared.UUID]string, len(byType))
		for nodeType, nrow := range byType {
			typeByNodeID[nrow.ID] = nodeType
		}
		callerSig := signalpkg.BuildTerminalSuccessSignal(t.Changed, t.AttributesDel, t.ChangeSummary, t.Tags)
		if err := bindEntryAliasEdgesToCaller(ctx, args, acq, tmplSpec, dispatched, typeByNodeID, callerSig, tx); err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: %w", err)
		}
		if err := seedEntryAliasSourcedAttributes(ctx, args, internalNodes, dispatched, typeByNodeID, merged, tx); err != nil {
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
			"calling_run_id":       acq.NodeRunID.String(),
			"settling_signal_type": settlingSig,
			"changed":              t.Changed,
			"child_count":          len(internalNodes),
		},
	}, tx); err != nil {
		return nil, err
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindSubgraphDispatched(),
		Payload: map[string]any{
			"caller_run_id":  acq.NodeRunID.String(),
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
			"calling_run_id", acq.NodeRunID.String(),
			"node_type", acq.NodeType,
			"delegate", acq.NodeDef.Delegate,
			"settling_signal_type", settlingSig)
	}
	nodeRunID := acq.NodeRunID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			NodeRunID:          nodeRunID,
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
			ParentNodeRunID:    scope.ParentNodeRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
	}
	return post, nil
}

// @concept: sub-graph
// @concept: delegation
func bindEntryAliasEdgesToCaller(
	ctx context.Context, args RunArgs, acq *acquisition, tmpl *node.TemplateSpec, dispatched []DispatchedChild, typeByNodeID map[shared.UUID]string, callerSig signalpkg.Signal, tx persistence.Tx,
) error {
	if tmpl == nil || acq.NodeDef == nil {
		return nil
	}
	entryAlias := ""
	for _, g := range tmpl.Graphs {
		if g.Name == acq.NodeDef.Delegate {
			entryAlias = g.Entry
			break
		}
	}
	if entryAlias == "" {
		return nil
	}
	edges, err := subscriptionEdgesForTemplate(ctx, args, acq.TemplateHash, tx)
	if err != nil {
		return fmt.Errorf("bindEntryAliasEdgesToCaller: edges: %w", err)
	}
	if edges == nil {
		return nil
	}
	matched := edges.Match(entryAlias, callerSig.Type)
	if len(matched) == 0 {
		return nil
	}
	childRunByType := make(map[string]shared.UUID, len(dispatched))
	for _, c := range dispatched {
		if nodeType, ok := typeByNodeID[c.NodeID]; ok {
			childRunByType[nodeType] = c.NodeRunID
		}
	}
	inserted := false
	for _, edge := range matched {
		if !edge.ResolvesViaCallingNode {
			continue
		}
		if edge.WhenExpr != nil {
			ok, _ := edge.WhenExpr.Eval(callerSig)
			if !ok {
				continue
			}
		}
		receiverRunID, ok := childRunByType[edge.ReceiverNodeType]
		if !ok {
			continue
		}
		if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           acq.FrameID,
			ReceiverNodeRunID: receiverRunID,
			SenderNodeRunID:   acq.NodeRunID,
			TopicKind:         waitSetTopicKindFor(edge.TypePattern),
		}, tx); err != nil {
			return fmt.Errorf("bindEntryAliasEdgesToCaller: wait-set insert for %s: %w", edge.ReceiverNodeType, err)
		}
		inserted = true
	}
	if !inserted {
		return nil
	}
	return args.Persist.WaitSet().MarkDrainedBySender(ctx, acq.FrameID, acq.NodeRunID, tx)
}

// @concept: sub-graph
// @concept: delegation
func seedEntryAliasSourcedAttributes(
	ctx context.Context, args RunArgs, internalNodes []node.TemplateNodeDef, dispatched []DispatchedChild, typeByNodeID map[shared.UUID]string, callerBag map[string]any, tx persistence.Tx,
) error {
	defByType := make(map[string]*node.TemplateNodeDef, len(internalNodes))
	for i := range internalNodes {
		defByType[internalNodes[i].Type] = &internalNodes[i]
	}
	var callerBagRaw json.RawMessage
	for _, c := range dispatched {
		def := defByType[typeByNodeID[c.NodeID]]
		if def == nil || def.Attributes == nil || len(def.Attributes.Schema) == 0 {
			continue
		}
		deps := map[string]json.RawMessage{}
		for _, s := range def.Subscribes {
			if !s.ResolvesViaCallingNode {
				continue
			}
			if callerBagRaw == nil {
				encoded, err := json.Marshal(callerBag)
				if err != nil {
					return fmt.Errorf("seedEntryAliasSourcedAttributes: encode caller bag: %w", err)
				}
				callerBagRaw = encoded
			}
			deps[s.Node] = callerBagRaw
		}
		if len(deps) == 0 {
			continue
		}
		rctx := attributes.ResolveContext{Deps: deps}
		props, _ := def.Attributes.Schema["properties"].(map[string]any)
		seeded := map[string]any{}
		for name, propAny := range props {
			prop, _ := propAny.(map[string]any)
			if prop == nil {
				continue
			}
			src, _ := prop["source"].(string)
			if src == "" {
				continue
			}
			val, err := attributes.SubstituteValue(src, rctx)
			if err != nil {
				continue
			}
			seeded[name] = val
		}
		if len(seeded) == 0 {
			continue
		}
		existing, err := args.Persist.NodeAttributes().GetByRun(ctx, c.NodeRunID, tx)
		if err != nil {
			return fmt.Errorf("seedEntryAliasSourcedAttributes: load child bag %s: %w", c.NodeRunID, err)
		}
		bag := map[string]any{}
		if existing != nil && existing.Data != nil {
			bag = existing.Data
		}
		for k, v := range seeded {
			bag[k] = v
		}
		if err := args.Persist.NodeAttributes().Upsert(ctx, c.NodeRunID, c.NodeID, bag, tx); err != nil {
			return fmt.Errorf("seedEntryAliasSourcedAttributes: upsert child bag %s: %w", c.NodeRunID, err)
		}
		if err := args.Persist.NodeAttributes().SetDispatchInputBag(ctx, c.NodeRunID, c.NodeID, bag, tx); err != nil {
			return fmt.Errorf("seedEntryAliasSourcedAttributes: persist child dispatch bag %s: %w", c.NodeRunID, err)
		}
	}
	return nil
}

// @concept: sub-graph
// @concept: delegation
// @concept: run-scope
func applyTerminalCompleteSubgraphExit(
	ctx context.Context, args RunArgs, acq *acquisition,
	merged map[string]any, tx persistence.Tx,
) (postCommitFn, error) {
	var wb []byte
	if len(merged) > 0 {
		encoded, err := json.Marshal(merged)
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphExit: encode writeback: %w", err)
		}
		wb = encoded
	}
	return SettleFromDelegate(ctx, args, DelegateSettlementInput{
		ExitNodeRunID: acq.NodeRunID,
		ExitNodeID:    acq.NodeID,
		ExitNodeAlias: acq.NodeType,
		InstanceID:    acq.InstanceID,
		Writeback:     wb,
	}, tx)
}

func isSubgraphExitNode(acq *acquisition) bool {
	if acq == nil || acq.NodeDef == nil {
		return false
	}
	return acq.NodeDef.IsSubgraphExit
}
