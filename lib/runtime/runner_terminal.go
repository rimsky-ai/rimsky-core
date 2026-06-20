// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type postCommitFn func(ctx context.Context)

func applyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	metricsOf(args).IncTerminal(string(terminalClassFor(t.Kind)), t.ErrorClass)
	var pc postCommitFn
	var err error
	switch t.Kind {
	case terminalKindComplete:
		pc, err = applyTerminalComplete(ctx, args, acq, resolvedAttrs, schema, t, tx)
	case terminalKindErrored:
		pc, err = applyTerminalError(ctx, args, acq, resolvedAttrs, t.ErrorClass, t.Payload, t.Tags, t.AttributesDel, t.Scratch, tx)
	case terminalKindInfra:
		pc, err = applyTerminalInfraError(ctx, args, acq, t.ErrorClass, t.Payload, t.Scratch, tx)
	case terminalKindPark:
		return applyTerminalPark(ctx, args, acq, resolvedAttrs, t, tx)
	default:
		return nil, fmt.Errorf("applyTerminal: unhandled terminal kind %v", t.Kind)
	}
	if err != nil {
		return nil, err
	}
	// @story: work-completed-emitted
	inner := pc
	kind := t.Kind
	return func(ctx context.Context) {
		if inner != nil {
			inner(ctx)
		}
		emitWorkCompleted(ctx, args, acq, kind)
	}, nil
}

func emitWorkCompleted(ctx context.Context, args RunArgs, acq *acquisition, kind terminalKind) {
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: events.KindWorkCompleted(), Payload: map[string]any{
				"supervisor_id": args.SupervisorID,
				"dispatch_id":   acq.DispatchID.String(),
				"terminal_kind": string(terminalClassFor(kind)),
			},
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("emitWorkCompleted: work_completed event append failed; pairing event lost",
			"node_id", acq.NodeID.String(),
			"dispatch_id", acq.DispatchID.String(),
			"terminal_kind", string(terminalClassFor(kind)),
			"error", err.Error())
	}
}

func runApplyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent,
	setup func(ctx context.Context, tx persistence.Tx) (skip bool, err error),
) error {
	var postCommit postCommitFn
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if setup != nil {
			skip, err := setup(ctx, tx)
			if err != nil {
				return err
			}
			if skip {
				return nil
			}
		}
		pc, err := applyTerminal(ctx, args, acq, resolvedAttrs, schema, t, tx)
		if err != nil {
			return err
		}
		postCommit = pc
		return nil
	}); err != nil {
		return err
	}
	if postCommit != nil {
		postCommit(ctx)
	}
	return nil
}

func terminalClassFor(k terminalKind) string {
	switch k {
	case terminalKindComplete:
		return "complete"
	case terminalKindErrored:
		return "errored"
	case terminalKindInfra:
		return "infra"
	case terminalKindPark:
		return "park"
	}
	return "unknown"
}

// @concept: signal
// @concept: sub-graph
// @concept: delegation
// @concept: terminal-tag
func applyTerminalComplete(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	merged := mergeAttributesDelta(resolvedAttrs, t.AttributesDel)
	if t.Changed && len(t.AttributesDel) > 0 && schema != nil {
		if err := attributes.Validate(schema, merged, attributes.PhaseCommit); err != nil {
			if appendErr := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: events.KindAttributesSchemaFailed(),
				Payload: map[string]any{
					"errors": []map[string]any{{"message": err.Error()}},
				},
			}, tx); appendErr != nil && args.Logger != nil {
				args.Logger.Warn("runner_terminal: append attributes_schema_failed event failed",
					"node_id", acq.NodeID.String(),
					"error", appendErr.Error())
			}
			// @concept: executor
			return applyErrorPolicyWithScratch(ctx, args, acq, "attributes_schema_failed",
				map[string]any{"error": err.Error()}, t.Tags, t.Scratch, tx)
		}
	}

	if acq.NodeDef != nil && acq.NodeDef.IsSubgraphEntryAbsorbed {
		return applyTerminalCompleteSubgraphCaller(ctx, args, acq, merged, t, tx)
	}

	isSubgraphExit := isSubgraphExitNode(acq)
	if isSubgraphExit {
		if err := applyTerminalCompleteSubgraphExit(ctx, args, acq, merged, tx); err != nil {
			return nil, err
		}
	}

	successType := string(signalpkg.TypePath("terminal/success"))
	settlingSignalType := &successType

	if err := releaseLocksInTx(ctx, args, tx, acq, true, false); err != nil {
		return nil, err
	}
	if !isSubgraphExit {
		if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
			return nil, fmt.Errorf("applyTerminalComplete: upsert attributes: %w", err)
		}
	}
	// @concept: executor
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, t.Scratch); err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: %w", err)
	}
	if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{}, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: clear error state: %w", err)
	}
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
		cascade.NodeStateFresh, cascade.ReasonHandlerComplete, settlingSignalType, tx); err != nil {
		return nil, err
	}
	// @concept: node-run
	// @concept: cascade
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: remove for node: %w", err)
	}
	// @concept: cascade
	// @concept: signal
	// @concept: wait-set
	visited := map[foundationshared.UUID]struct{}{}
	successSig := signalpkg.Signal{
		Type: "terminal/success",
		Payload: map[string]any{
			"changed":          t.Changed,
			"attributes_delta": orEmptyMap(t.AttributesDel),
			"change_summary":   t.ChangeSummary,
			"tags":             t.Tags,
		},
	}
	if err := emitSignalInTx(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, successSig, visited); err != nil {
		return nil, err
	}
	for key, value := range merged {
		attrSig := signalpkg.Signal{
			Type: signalpkg.TypePath(fmt.Sprintf("attribute/%s/changed", key)),
			Payload: map[string]any{
				"key":   key,
				"value": value,
			},
		}
		if err := cascadeSubscribersStaleInTxWithVisited(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, attrSig, visited); err != nil {
			return nil, err
		}
	}
	// @concept: wait-set
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, err
	}

	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		if len(t.AttributesDel) > 0 {
			if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				for key, value := range t.AttributesDel {
					attrSig := signalpkg.Signal{
						Type: signalpkg.TypePath(fmt.Sprintf("attribute/%s/changed", key)),
						Payload: map[string]any{
							"key":   key,
							"value": value,
						},
					}
					if err := signalaudit.EmitSignal(ctx, args.Persist.Events(),
						acq.InstanceID, acq.NodeID, attrSig, args.Clock.Now(), tx); err != nil {
						return err
					}
				}
				return nil
			}); err != nil && args.Logger != nil {
				args.Logger.Warn("runner_terminal: append attribute signal rows failed",
					"node_id", acq.NodeID.String(),
					"error", err.Error())
			}
		}
		fanoutRecalculate(ctx, args, acq)
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			RunID:              dispatchID,
			NodeID:             acq.NodeID,
			State:              string(cascade.NodeStateFresh),
			SettlingSignalType: *settlingSignalType,
			Changed:            t.Changed,
			TerminalKind:       "complete",
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
		if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
			cascade.NodeStateFresh, settlingSignalType); err != nil {
			args.Logger.Warn("applyTerminalComplete: run-tree propagation failed",
				"run_id", dispatchID.String(), "error", err.Error())
		}
	}
	return post, nil
}

// @concept: cascade
// @concept: signal
// @concept: wait-set
func cascadeSubscribersStaleInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
) error {
	return cascadeSubscribersStaleInTxWithVisited(ctx, args, tx,
		senderID, senderNodeType, senderRunID, instanceID, senderFrameID, sig,
		map[foundationshared.UUID]struct{}{})
}

func cascadeSubscribersStaleInTxWithVisited(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
	visitedReceivers map[foundationshared.UUID]struct{},
) error {
	inst, err := args.Persist.Instances().Get(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: get instance: %w", err)
	}
	if inst == nil {
		return nil
	}
	edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: edges: %w", err)
	}
	if edges == nil {
		return nil
	}
	instNodes, err := args.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: list instance nodes: %w", err)
	}
	byType := make(map[string][]persistence.NodeRow, len(instNodes))
	for _, n := range instNodes {
		byType[n.NodeType] = append(byType[n.NodeType], n)
	}
	// @concept: run-scope
	senderRun, err := args.Persist.RunTree().GetByID(ctx, tx, senderRunID)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: load sender run: %w", err)
	}
	if senderRun == nil {
		return nil
	}
	senderRunScopeID := senderRun.RunScopeID
	senderRunScope, err := args.Persist.RunScopes().GetByID(ctx, tx, senderRunScopeID)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: load sender run scope: %w", err)
	}
	if senderRunScope == nil {
		return nil
	}
	senderScopeIsMain := senderRunScope.ParentRunID == nil
	// @concept: signal
	candidateEdges := edges.Match(senderNodeType, sig.Type)
	if len(candidateEdges) == 0 {
		return nil
	}
	type walkItem struct {
		nodeID   foundationshared.UUID
		nodeType string
		runID    foundationshared.UUID
	}
	cur := walkItem{nodeID: senderID, nodeType: senderNodeType, runID: senderRunID}
	visited := map[foundationshared.UUID]struct{}{senderID: {}}
	{
		for _, edge := range candidateEdges {
			if edge.WhenExpr != nil {
				ok, _ := edge.WhenExpr.Eval(sig)
				if !ok {
					continue
				}
			}
			receivers := byType[edge.ReceiverNodeType]
			for _, r := range receivers {
				// @concept: cascade
				// @concept: node-subscription
				// @concept: parked-state
				// @concept: run-scope
				receiverRunScopeID := senderRunScopeID
				if !senderScopeIsMain {
					existingID, existingOK, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: probe receiver run %s: %w", r.ID, err)
					}
					if !existingOK {
						continue
					}
					_ = existingID
				}
				// @decision: wake-on-change-wait-set-only
				skipAffirm := false
				if !edge.WakeOnChange {
					skipAffirm = true
				} else if _, seen := visitedReceivers[r.ID]; seen {
					skipAffirm = true
				} else {
					visitedReceivers[r.ID] = struct{}{}
					if r.ID != senderID {
						settled, err := args.Persist.Nodes().HasRunForNodeInFrame(ctx, r.ID, senderFrameID, tx)
						if err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: probe receiver frame %s: %w", r.ID, err)
						}
						if settled {
							skipAffirm = true
						}
					}
				}
				var receiverRunID foundationshared.UUID
				if skipAffirm {
					existingID, hasInFlight, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver run (skip-affirm) %s: %w", r.ID, err)
					}
					if !hasInFlight {
						continue
					}
					receiverRunID = existingID
				} else {
					if err := args.Persist.Nodes().AffirmNodeRunRow(ctx, r.ID, receiverRunScopeID, senderFrameID, tx); err != nil {
						if errors.Is(err, persistence.ErrRunScopeClosed) {
							continue
						}
						return fmt.Errorf("cascadeSubscribersStaleInTx: affirm receiver run %s: %w", r.ID, err)
					}
					resolvedID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver run %s: %w", r.ID, err)
					}
					if !ok {
						continue
					}
					receiverRunID = resolvedID
					if r.State == cascade.NodeStateParked {
						if err := wakeParkedReceiverInTx(ctx, args, tx, r, senderFrameID); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: wake parked %s: %w", r.ID, err)
						}
					} else {
						if err := args.Persist.Nodes().MarkStaleForCascade(ctx, receiverRunID, senderFrameID, tx); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: mark stale %s: %w", r.ID, err)
						}
					}
				}
				if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
					FrameID:           senderFrameID,
					ReceiverRunID:     receiverRunID,
					SenderRunID:       cur.runID,
					TopicKind:         waitSetTopicKindFor(edge.TypePattern),
					SubscriptionScope: edge.SubscriptionScope,
				}, tx); err != nil {
					return fmt.Errorf("cascadeSubscribersStaleInTx: wait-set insert: %w", err)
				}
				if err := pullForceRefreshUpstreams(ctx, args, tx, r, byType, receiverRunID, receiverRunScopeID, senderFrameID, inst.TemplateHash, visited); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// @concept: cascade
// @concept: attribute
func pullForceRefreshUpstreams(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	receiver persistence.NodeRow,
	byType map[string][]persistence.NodeRow,
	receiverRunID foundationshared.UUID,
	targetRunScopeID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	templateHash string,
	visited map[foundationshared.UUID]struct{},
) error {
	refreshEdges, err := hardDepEdgesForTemplate(ctx, args, templateHash, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: upstream-refresh edges: %w", err)
	}
	if len(refreshEdges) == 0 {
		return nil
	}
	if len(refreshEdges[receiver.NodeType]) == 0 {
		return nil
	}
	for _, upstreamType := range refreshEdges[receiver.NodeType] {
		upstreamNodes := byType[upstreamType]
		if len(upstreamNodes) == 0 {
			continue
		}
		upstreamNode := upstreamNodes[0]

		if _, seen := visited[upstreamNode.ID]; seen {
			continue
		}

		// @concept: parked-state
		// @concept: cascade
		// @story: resume-preserves-snapshot
		upstreamRunScopeID := targetRunScopeID
		_, hasInFlightRun, err := args.Queue.GetInFlightRunForNode(
			ctx, tx, upstreamNode.ID, upstreamRunScopeID,
		)
		if err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: probe in-flight upstream-refresh upstream %s: %w",
				upstreamType, err)
		}
		if !hasInFlightRun {
			settledThisFrame, err := args.Persist.Nodes().HasRunForNodeInFrame(
				ctx, upstreamNode.ID, senderFrameID, tx,
			)
			if err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: probe settled upstream-refresh upstream %s: %w",
					upstreamType, err)
			}
			if settledThisFrame {
				visited[upstreamNode.ID] = struct{}{}
				continue
			}
		}

		// @concept: run-scope
		if err := args.Persist.Nodes().AffirmNodeRunRow(ctx, upstreamNode.ID, upstreamRunScopeID, senderFrameID, tx); err != nil {
			if errors.Is(err, persistence.ErrRunScopeClosed) {
				continue
			}
			return fmt.Errorf("cascadeSubscribersStaleInTx: affirm upstream %s: %w", upstreamType, err)
		}
		upstreamRunID, hasRun, err := args.Queue.GetInFlightRunForNode(
			ctx, tx, upstreamNode.ID, upstreamRunScopeID,
		)
		if err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: get in-flight upstream %s: %w",
				upstreamType, err)
		}

		if !hasRun {
			if err := stalemarkAndEnqueueInFrame(
				ctx, args, tx, &upstreamNode, upstreamRunScopeID, senderFrameID,
			); err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: stale-mark upstream %s: %w",
					upstreamType, err)
			}
			upstreamRunID, hasRun, err = args.Queue.GetInFlightRunForNode(
				ctx, tx, upstreamNode.ID, upstreamRunScopeID,
			)
			if err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: re-fetch in-flight upstream %s after stale-mark: %w",
					upstreamType, err)
			}
			if !hasRun {
				return fmt.Errorf("cascadeSubscribersStaleInTx: upstream %s not in-flight after stale-mark",
					upstreamType)
			}
		}

		visited[upstreamNode.ID] = struct{}{}

		if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           senderFrameID,
			ReceiverRunID:     receiverRunID,
			SenderRunID:       upstreamRunID,
			TopicKind:         "attribute",
			SubscriptionScope: "direct",
		}, tx); err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: insert upstream-refresh wait-set: %w", err)
		}
	}
	return nil
}

// @concept: wait-set
func drainWaitSetOnSettled(
	ctx context.Context, args RunArgs, tx persistence.Tx, frameID, senderRunID foundationshared.UUID,
) error {
	return args.Persist.WaitSet().MarkDrainedBySender(ctx, frameID, senderRunID, tx)
}

func fanoutRecalculate(ctx context.Context, args RunArgs, acq *acquisition) {
	var receivers []persistence.NodeRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := args.Persist.Instances().Get(ctx, acq.InstanceID, tx)
		if err != nil || inst == nil {
			return err
		}
		edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
		if err != nil || edges == nil {
			return err
		}
		receiverTypeList := edges.ReceiverNodeTypesForSender(acq.NodeType)
		if len(receiverTypeList) == 0 {
			return nil
		}
		receiverTypes := make(map[string]struct{}, len(receiverTypeList))
		for _, t := range receiverTypeList {
			receiverTypes[t] = struct{}{}
		}
		instNodes, err := args.Persist.Nodes().ListByInstance(ctx, acq.InstanceID, tx)
		if err != nil {
			return err
		}
		for _, n := range instNodes {
			if n.ID == acq.NodeID {
				continue
			}
			if _, ok := receiverTypes[n.NodeType]; ok {
				receivers = append(receivers, n)
			}
		}
		return nil
	}); err != nil {
		return
	}
	src := acq.NodeID
	for _, r := range receivers {
		_ = RecalculateNode(ctx, RecalculateArgs{
			Persist:      args.Persist,
			Queue:        args.Queue,
			Clock:        args.Clock,
			Logger:       args.Logger,
			SourceNodeID: &src,
			TargetNodeID: r.ID,
		})
	}
}

func upsertFinalAttributesTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, merged map[string]any,
) error {
	prior, _ := args.Persist.NodeAttributes().GetByRun(ctx, acq.DispatchID, tx)
	final := merged
	if prior != nil && len(prior.Data) > 0 {
		combined := make(map[string]any, len(prior.Data)+len(merged))
		for k, v := range prior.Data {
			combined[k] = v
		}
		for k, v := range merged {
			combined[k] = v
		}
		final = combined
	}
	if final == nil {
		final = map[string]any{}
	}
	return args.Persist.NodeAttributes().Upsert(ctx, acq.DispatchID, acq.NodeID, final, tx)
}

func mergeAttributesDelta(base, delta map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(delta))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range delta {
		out[k] = v
	}
	return out
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func waitSetTopicKindFor(pattern signalpkg.TypePath) string {
	switch pattern.TopLevel() {
	case signalpkg.KindTerminal:
		return "terminal"
	case signalpkg.KindTransient:
		return "transient"
	case signalpkg.KindAttribute:
		return "attribute"
	default:
		return "state"
	}
}
