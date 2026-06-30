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
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
)

type postCommitFn func(ctx context.Context)

func chainPostCommit(a, b postCommitFn) postCommitFn {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context) {
		a(ctx)
		b(ctx)
	}
}

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
		return applyTerminalPark(ctx, args, acq, t, tx)
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
			return applyErrorPolicyWithScratch(ctx, args, acq, "attributes_schema_failed", "",
				map[string]any{"error": err.Error()}, t.Tags, t.AttributesDel, t.Scratch, tx)
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

	// @concept: claim-handle
	// @decision: held-as-state-not-phase
	heldBeforeRelease, err := runHasActiveHeldClaims(ctx, args, tx, acq.DispatchID)
	if err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: held probe (pre-release): %w", err)
	}

	releasePC, err := releaseLocksInTx(ctx, args, tx, acq, true, false)
	if err != nil {
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
	if heldBeforeRelease {
		// @concept: claim-handle
		// @decision: held-as-state-not-phase
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
			cascade.NodeStateHeld, cascade.ReasonHandlerHeld, settlingSignalType, tx); err != nil {
			return nil, err
		}
		if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
			return nil, fmt.Errorf("applyTerminalComplete: remove for node (held): %w", err)
		}
		tmplSpec, terr := loadTemplateSpec(ctx, args, tx, acq.InstanceID)
		if terr != nil {
			return nil, fmt.Errorf("applyTerminalComplete: load template for held filter: %w", terr)
		}
		heldFilter := subgraphMemberFilter(tmplSpec, acq.NodeType)
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
		if err := emitSignalInTxWithFilter(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
			successSig, visited, heldFilter); err != nil {
			return nil, err
		}
		if err := emitAttributeChangesForRunInTx(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
			visited, heldFilter); err != nil {
			return nil, err
		}
		if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
			return nil, err
		}
		// @decision: held-as-state-not-phase
		transitionPC, err := transitionThisHolderIfFullyResolved(ctx, args, tx, acq)
		if err != nil {
			return nil, err
		}
		dispatchID := acq.DispatchID
		post := func(ctx context.Context) {
			fanoutRecalculate(ctx, args, acq)
			scope := resolveAcqScope(ctx, args, acq)
			EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
				InstanceID:       acq.InstanceID,
				FrameID:          acq.FrameID,
				RunID:            dispatchID,
				NodeID:           acq.NodeID,
				State:            string(cascade.NodeStateHeld),
				Changed:          t.Changed,
				TerminalKind:     "complete",
				NodeAlias:        acq.NodeType,
				ExecutorName:     acq.Executor,
				TemplateHash:     acq.TemplateHash,
				Params:           acq.InstanceParams,
				AttributesMerged: acq.MergedAttributes,
				HeldClaims:       HeldClaimsForLineage(acq),
				ParentRunID:      scope.ParentRunID,
				ChildKey:         scope.PartitionKey,
				SubstitutionRefs: CollectSubstitutionRefsForEmit(ctx, args, acq),
			})
		}
		return chainPostCommit(chainPostCommit(releasePC, transitionPC), post), nil
	}
	// @concept: claim-handle
	// @decision: held-as-state-not-phase
	portfolio, err := evaluateHolderPortfolio(ctx, args, tx, acq.DispatchID)
	if err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: portfolio probe: %w", err)
	}
	if portfolio.Poisoned {
		return applyTerminalCompletePoisoned(ctx, args, acq, t, tx)
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
	if err := emitAttributeChangesForRunInTx(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
		visited, nil); err != nil {
		return nil, err
	}
	// @concept: wait-set
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, err
	}

	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
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
	return chainPostCommit(releasePC, post), nil
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
// @decision: terminal-error-abandoned-as-error-class
func applyTerminalCompletePoisoned(
	ctx context.Context, args RunArgs, acq *acquisition,
	t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	abandonedType := string(signalpkg.TypePath("terminal/error/abandoned"))
	settlingSignalType := &abandonedType
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
		cascade.NodeStateFailed, cascade.ReasonAutoTerminalAbandon, settlingSignalType, tx); err != nil {
		return nil, err
	}
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalCompletePoisoned: remove for node: %w", err)
	}
	tmplSpec, terr := loadTemplateSpec(ctx, args, tx, acq.InstanceID)
	if terr != nil {
		return nil, fmt.Errorf("applyTerminalCompletePoisoned: load template: %w", terr)
	}
	nonMemberFilter := subgraphNonMemberFilter(tmplSpec, acq.NodeType)
	visited := map[foundationshared.UUID]struct{}{}
	abandonedSig := signalpkg.BuildTerminalErrorSignal(
		"abandoned",
		nil,
		0, 0,
		t.AttributesDel,
		t.Tags,
	)
	if err := emitSignalInTxWithFilter(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
		abandonedSig, visited, nonMemberFilter); err != nil {
		return nil, err
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
		visited, nonMemberFilter); err != nil {
		return nil, err
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, err
	}
	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			RunID:              dispatchID,
			NodeID:             acq.NodeID,
			State:              string(cascade.NodeStateFailed),
			SettlingSignalType: *settlingSignalType,
			Changed:            t.Changed,
			TerminalKind:       "errored",
			ErrorClass:         "abandoned",
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
			cascade.NodeStateFailed, settlingSignalType); err != nil {
			args.Logger.Warn("applyTerminalCompletePoisoned: run-tree propagation failed",
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
		map[foundationshared.UUID]struct{}{}, nil)
}

// @concept: cascade
// @decision: held-as-state-not-phase
type receiverFilter func(receiverNodeType string) bool

func cascadeSubscribersStaleInTxWithVisited(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
	visitedReceivers map[foundationshared.UUID]struct{},
	filter receiverFilter,
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
	for _, edge := range candidateEdges {
		if edge.WhenExpr != nil {
			ok, _ := edge.WhenExpr.Eval(sig)
			if !ok {
				continue
			}
		}
		if filter != nil && !filter(edge.ReceiverNodeType) {
			continue
		}
		receivers := byType[edge.ReceiverNodeType]
		for _, r := range receivers {
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
			receiverRunID, hasReceiver, err := resolveReceiverRunForCascade(
				ctx, args, tx,
				r.ID, receiverRunScopeID, senderFrameID, senderID, senderRunID,
				visitedReceivers,
			)
			if err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver %s: %w", r.ID, err)
			}
			if !hasReceiver {
				continue
			}
			if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
				FrameID:           senderFrameID,
				ReceiverRunID:     receiverRunID,
				SenderRunID:       senderRunID,
				TopicKind:         waitSetTopicKindFor(edge.TypePattern),
				SubscriptionScope: edge.SubscriptionScope,
			}, tx); err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: wait-set insert: %w", err)
			}
			pullVisited := map[foundationshared.UUID]struct{}{r.ID: {}, senderID: {}}
			if err := pullForceRefreshUpstreams(ctx, args, tx, r, byType, receiverRunID, receiverRunScopeID, senderFrameID, inst.TemplateHash, pullVisited); err != nil {
				return err
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
		return fmt.Errorf("pullForceRefreshUpstreams: upstream-refresh edges: %w", err)
	}
	if len(refreshEdges) == 0 || len(refreshEdges[receiver.NodeType]) == 0 {
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
		visited[upstreamNode.ID] = struct{}{}
		upstreamRunScopeID := targetRunScopeID
		upstreamRunID, hasInFlight, err := args.Queue.GetInFlightRunForNode(
			ctx, tx, upstreamNode.ID, upstreamRunScopeID,
		)
		if err != nil {
			return fmt.Errorf("pullForceRefreshUpstreams: probe upstream %s: %w", upstreamType, err)
		}
		if !hasInFlight {
			settledThisFrame, err := args.Persist.Nodes().HasRunForNodeInFrame(
				ctx, upstreamNode.ID, senderFrameID, tx,
			)
			if err != nil {
				return fmt.Errorf("pullForceRefreshUpstreams: probe settled upstream %s: %w", upstreamType, err)
			}
			if settledThisFrame {
				continue
			}
			newID, err := args.Persist.Nodes().CreateCascadePending(ctx, tx, upstreamNode.ID, upstreamRunScopeID, senderFrameID)
			if err != nil {
				if errors.Is(err, persistence.ErrRunScopeClosed) {
					continue
				}
				return fmt.Errorf("pullForceRefreshUpstreams: create pending upstream %s: %w", upstreamType, err)
			}
			upstreamRunID = newID
			if err := evaluateOneGate(ctx, args, tx, newID); err != nil {
				return fmt.Errorf("pullForceRefreshUpstreams: evaluate gate for upstream %s: %w", upstreamType, err)
			}
		}
		if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           senderFrameID,
			ReceiverRunID:     receiverRunID,
			SenderRunID:       upstreamRunID,
			TopicKind:         "attribute",
			SubscriptionScope: "direct",
		}, tx); err != nil {
			return fmt.Errorf("pullForceRefreshUpstreams: insert wait-set: %w", err)
		}
	}
	return nil
}

// @concept: wait-set
// @concept: cascade
func drainWaitSetOnSettled(
	ctx context.Context, args RunArgs, tx persistence.Tx, frameID, senderRunID foundationshared.UUID,
) error {
	if err := args.Persist.WaitSet().MarkDrainedBySender(ctx, frameID, senderRunID, tx); err != nil {
		return err
	}
	if err := evaluateGatesAfterDrain(ctx, args, tx, frameID, senderRunID); err != nil {
		return err
	}
	siblings, err := args.Persist.Nodes().ListPendingSiblingRunsInScope(ctx, tx, senderRunID)
	if err != nil {
		return fmt.Errorf("drainWaitSetOnSettled: list pending siblings: %w", err)
	}
	for _, sib := range siblings {
		if err := evaluateOneGate(ctx, args, tx, sib); err != nil {
			return fmt.Errorf("drainWaitSetOnSettled: evaluate sibling gate %s: %w", sib, err)
		}
	}
	return nil
}

func fanoutRecalculate(ctx context.Context, args RunArgs, acq *acquisition) {
	if !IsFanOutNode(acq.NodeDef) {
		return
	}
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
