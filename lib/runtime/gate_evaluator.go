// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
)

// @concept: cascade
// @decision: mode-default-most-recent
func evaluateGatesAfterDrain(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	frameID, senderRunID foundationshared.UUID,
) error {
	receiverIDs, err := args.Persist.WaitSet().ListPendingReceiversForDrainedSender(ctx, frameID, senderRunID, tx)
	if err != nil {
		return fmt.Errorf("evaluateGatesAfterDrain: list receivers: %w", err)
	}
	for _, receiverID := range receiverIDs {
		if err := evaluateOneGate(ctx, args, tx, receiverID); err != nil {
			return fmt.Errorf("evaluateGatesAfterDrain: receiver %s: %w", receiverID, err)
		}
	}
	return nil
}

// @concept: cascade
// @concept: cascade-mode
// @decision: mode-default-most-recent
func evaluateOneGate(
	ctx context.Context, args RunArgs, tx persistence.Tx, receiverRunID foundationshared.UUID,
) error {
	row, err := args.Persist.Nodes().GetRunForGate(ctx, tx, receiverRunID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if row == nil || row.State != cascade.NodeStatePending {
		return nil
	}
	undrained, err := args.Persist.WaitSet().HasUndrainedRowsForReceiver(ctx, row.FrameID, row.RunID, tx)
	if err != nil {
		return fmt.Errorf("undrained probe: %w", err)
	}
	if undrained {
		return nil
	}
	upstreamInFlight, err := anySubscribedUpstreamInFlight(ctx, args, tx, row)
	if err != nil {
		return fmt.Errorf("upstream probe: %w", err)
	}
	if upstreamInFlight {
		return nil
	}
	bag, priorBagFields, err := buildPendingInputBagSplit(ctx, args, tx, row)
	if err != nil {
		return fmt.Errorf("build bag: %w", err)
	}
	mode, err := args.Persist.Nodes().GetCascadeMode(ctx, row.NodeID, tx)
	if err != nil {
		return fmt.Errorf("get cascade mode: %w", err)
	}
	drop, err := applyCascadeModeRule(ctx, args, tx, row, bag, mode)
	if err != nil {
		return fmt.Errorf("mode rule: %w", err)
	}
	if drop {
		return args.Persist.Nodes().DropPendingRun(ctx, tx, row.RunID)
	}
	resolved, err := buildResolvedBagAtGateEvalCarry(ctx, args, tx, row, bag, priorBagFields)
	if err != nil {
		if isSubstitutionClassifiableError(err) {
			return routeSubstitutionFailureAtGate(ctx, args, tx, row, err)
		}
		return fmt.Errorf("resolve at gate-eval: %w", err)
	}
	if err := args.Persist.NodeAttributes().Upsert(ctx, row.RunID, row.NodeID, resolved, tx); err != nil {
		return fmt.Errorf("seed live bag: %w", err)
	}
	if err := args.Persist.NodeAttributes().SetDispatchInputBag(ctx, tx, row.RunID, row.NodeID, resolved); err != nil {
		return fmt.Errorf("persist dispatch bag: %w", err)
	}
	return args.Persist.Nodes().TransitionPendingToStale(ctx, tx, row.RunID, args.Clock.Now())
}

// @concept: attribute
func isSubstitutionClassifiableError(err error) bool {
	var miss *attributes.ErrMissingSource
	if errors.As(err, &miss) {
		return true
	}
	var validation *attributeValidationError
	return errors.As(err, &validation)
}

// @concept: cascade
// @concept: attribute
// @decision: substitution-failure-routes-with-substitution
func routeSubstitutionFailureAtGate(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row *persistence.NodeRunForGate, subErr error,
) error {
	receiverNode, err := args.Persist.Nodes().Get(ctx, row.NodeID, tx)
	if err != nil {
		return fmt.Errorf("route substitution failure: load receiver node: %w", err)
	}
	if receiverNode == nil {
		return nil
	}
	tmplSpec, err := loadTemplateSpec(ctx, args, tx, receiverNode.InstanceID)
	if err != nil {
		return fmt.Errorf("route substitution failure: load template: %w", err)
	}
	var nodeDef *node.TemplateNodeDef
	if tmplSpec != nil {
		nodeDef = lookupNodeDef(tmplSpec, receiverNode.NodeType)
	}
	class, eventKind := classifyAttributeFailure(subErr)
	directive := extractDirective(subErr)
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID:     &row.NodeID,
		InstanceID: &receiverNode.InstanceID,
		Kind:       eventKind,
		Payload: map[string]any{
			"directive": directive,
			"site":      "attribute",
			"field":     "",
			"reason":    subErr.Error(),
		},
	}, tx); err != nil {
		return fmt.Errorf("route substitution failure: append event: %w", err)
	}
	if err := args.Persist.Nodes().TransitionPendingToStale(ctx, tx, row.RunID, args.Clock.Now()); err != nil {
		return fmt.Errorf("route substitution failure: pending->stale: %w", err)
	}
	acq := &acquisition{
		DispatchID: row.RunID,
		NodeID:     row.NodeID,
		InstanceID: receiverNode.InstanceID,
		NodeType:   receiverNode.NodeType,
		Executor:   receiverNode.Executor,
		RunScopeID: row.RunScopeID,
		FrameID:    row.FrameID,
		NodeDef:    nodeDef,
	}
	if _, err := applyErrorPolicy(ctx, args, acq, class,
		map[string]any{"error": subErr.Error()}, tx); err != nil {
		return fmt.Errorf("route substitution failure: applyErrorPolicy: %w", err)
	}
	return nil
}

// @concept: cascade
func buildResolvedBagAtGateEvalCarry(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row *persistence.NodeRunForGate, senderKeyedBag map[string]any, carryForward map[string]any,
) (map[string]any, error) {
	receiverNode, err := args.Persist.Nodes().Get(ctx, row.NodeID, tx)
	if err != nil || receiverNode == nil {
		if err != nil {
			return nil, err
		}
		return senderKeyedBag, nil
	}
	tmplSpec, err := loadTemplateSpec(ctx, args, tx, receiverNode.InstanceID)
	if err != nil {
		return nil, err
	}
	if tmplSpec == nil {
		return senderKeyedBag, nil
	}
	nodeDef := lookupNodeDef(tmplSpec, receiverNode.NodeType)
	if nodeDef == nil || nodeDef.Attributes == nil {
		return map[string]any{}, nil
	}
	schema := schemaForGateEval(args, receiverNode.Executor, tmplSpec, nodeDef)
	if schema == nil {
		return map[string]any{}, nil
	}
	deps, err := senderKeyedBagToDeps(senderKeyedBag)
	if err != nil {
		return nil, err
	}
	inst, err := args.Persist.Instances().Get(ctx, receiverNode.InstanceID, tx)
	if err != nil {
		return nil, err
	}
	var paramsRaw json.RawMessage
	if inst != nil && len(inst.Params) > 0 {
		paramsRaw, err = json.Marshal(inst.Params)
		if err != nil {
			return nil, err
		}
	}
	var registryTypes map[string]struct{}
	if inst != nil {
		registryTypes = declaredMessageTypesForTemplate(ctx, args, inst.TemplateHash, tx)
	}
	rctx := attributes.ResolveContext{
		Deps:                  deps,
		Params:                paramsRaw,
		RegistryDeclaredTypes: registryTypes,
	}
	return substituteAttributesSchemaWith(schema, rctx, carryForward, true)
}

// @concept: attribute
func schemaForGateEval(args RunArgs, executor string, _ *node.TemplateSpec, nodeDef *node.TemplateNodeDef) map[string]any {
	var nodeSchema map[string]any
	if nodeDef != nil && nodeDef.Attributes != nil {
		nodeSchema = nodeDef.Attributes.Schema
	}
	var execSchema map[string]any
	if args.ExpectedAttributesSchemaFor != nil && executor != "" {
		if bytesIn, ok := args.ExpectedAttributesSchemaFor(executor); ok && len(bytesIn) > 0 {
			_ = json.Unmarshal(bytesIn, &execSchema)
		}
	}
	if execSchema == nil && nodeSchema == nil {
		return nil
	}
	return node.MergeAttributeDefaults(execSchema, nil, nodeSchema)
}

// @concept: attribute
func senderKeyedBagToDeps(bag map[string]any) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(bag))
	for k, v := range bag {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out[k] = raw
	}
	return out, nil
}

// @concept: cascade
// @decision: held-as-state-not-phase
func anySubscribedUpstreamInFlight(
	ctx context.Context, args RunArgs, tx persistence.Tx, row *persistence.NodeRunForGate,
) (bool, error) {
	receiverNode, err := args.Persist.Nodes().Get(ctx, row.NodeID, tx)
	if err != nil || receiverNode == nil {
		return false, err
	}
	inst, err := args.Persist.Instances().Get(ctx, receiverNode.InstanceID, tx)
	if err != nil || inst == nil {
		return false, err
	}
	edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
	if err != nil || edges == nil {
		return false, err
	}
	senderTypes := edges.SenderNodeTypesForReceiver(receiverNode.NodeType)
	if len(senderTypes) == 0 {
		return false, nil
	}
	senderTypeSet := make(map[string]struct{}, len(senderTypes))
	for _, t := range senderTypes {
		senderTypeSet[t] = struct{}{}
	}
	instNodes, err := args.Persist.Nodes().ListByInstance(ctx, receiverNode.InstanceID, tx)
	if err != nil {
		return false, err
	}
	var senderIDs []foundationshared.UUID
	nodeTypeByID := make(map[foundationshared.UUID]string, len(instNodes))
	for _, n := range instNodes {
		nodeTypeByID[n.ID] = n.NodeType
		if n.ID == row.NodeID {
			continue
		}
		if _, ok := senderTypeSet[n.NodeType]; ok {
			senderIDs = append(senderIDs, n.ID)
		}
	}
	if len(senderIDs) == 0 {
		return false, nil
	}
	tmplSpec, err := loadTemplateSpec(ctx, args, tx, receiverNode.InstanceID)
	if err != nil {
		return false, err
	}
	coMembers := subgraphMembersIncludingType(tmplSpec, receiverNode.NodeType)
	phases, err := args.Queue.ListInFlightRunStates(ctx, tx, senderIDs, row.FrameID, row.RunScopeID)
	if err != nil {
		return false, err
	}
	for senderID, p := range phases {
		senderType := nodeTypeByID[senderID]
		_, isCoMember := coMembers[senderType]
		for _, ph := range p {
			state := cascade.NodeState(ph)
			if state == cascade.NodeStateHeld && isCoMember {
				continue
			}
			if cascade.IsInFlight(state) {
				return true, nil
			}
		}
	}
	return false, nil
}

// @concept: cascade
// @decision: mode-default-most-recent
func buildPendingInputBagSplit(
	ctx context.Context, args RunArgs, tx persistence.Tx, row *persistence.NodeRunForGate,
) (map[string]any, map[string]any, error) {
	bag := map[string]any{}
	priorBagFields := map[string]any{}
	prior, err := args.Persist.Nodes().GetPriorRunBySequence(ctx, tx, row.NodeID, row.RunScopeID, row.Sequence)
	if err != nil {
		return nil, nil, fmt.Errorf("prior run: %w", err)
	}
	if prior != nil {
		priorBag, err := args.Persist.NodeAttributes().GetByRun(ctx, prior.RunID, tx)
		if err != nil {
			return nil, nil, fmt.Errorf("prior attrs: %w", err)
		}
		if priorBag != nil {
			for k, v := range priorBag.Data {
				bag[k] = v
				priorBagFields[k] = v
			}
		}
	}
	drained, err := args.Persist.WaitSet().ListDrainedAttributeRowsForReceiver(ctx, row.FrameID, row.RunID, tx)
	if err != nil {
		return nil, nil, fmt.Errorf("drained rows: %w", err)
	}
	for _, w := range drained {
		senderRun, err := args.Persist.RunTree().GetByID(ctx, tx, w.SenderRunID)
		if err != nil {
			return nil, nil, fmt.Errorf("sender run: %w", err)
		}
		if senderRun == nil {
			continue
		}
		senderAttrs, err := args.Persist.NodeAttributes().GetByRun(ctx, w.SenderRunID, tx)
		if err != nil {
			return nil, nil, fmt.Errorf("sender attrs: %w", err)
		}
		if senderAttrs == nil {
			continue
		}
		senderNode, err := args.Persist.Nodes().Get(ctx, senderRun.NodeID, tx)
		if err != nil || senderNode == nil {
			continue
		}
		bag[senderNode.NodeType] = senderAttrs.Data
	}
	return bag, priorBagFields, nil
}

// @concept: cascade
// @concept: cascade-mode
// @decision: mode-default-most-recent
func applyCascadeModeRule(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row *persistence.NodeRunForGate, bag map[string]any, mode cascade.CascadeMode,
) (drop bool, err error) {
	switch mode {
	case cascade.CascadeModeMostRecent, "":
		if _, derr := args.Persist.Nodes().DeletePriorCascadeStales(ctx, tx, row.NodeID, row.RunScopeID, row.Sequence); derr != nil {
			return false, fmt.Errorf("most-recent: delete prior: %w", derr)
		}
		return false, nil
	case cascade.CascadeModeSequenced:
		return false, nil
	case cascade.CascadeModeIdempotentQueue:
		return modeDropIfPriorEqual(ctx, args, tx, row, bag, false)
	case cascade.CascadeModeIdempotentSettled:
		return modeDropIfPriorEqual(ctx, args, tx, row, bag, true)
	}
	return false, fmt.Errorf("applyCascadeModeRule: unknown mode %q", mode)
}

// @concept: cascade
func modeDropIfPriorEqual(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row *persistence.NodeRunForGate, bag map[string]any, includeSettled bool,
) (bool, error) {
	priorStale, err := args.Persist.Nodes().GetPriorCascadeStaleNotClaimed(ctx, tx, row.NodeID, row.RunScopeID, row.Sequence)
	if err != nil {
		return false, fmt.Errorf("prior stale: %w", err)
	}
	if priorStale != nil {
		return bagsEqual(ctx, args, tx, priorStale.RunID, bag)
	}
	if !includeSettled {
		return false, nil
	}
	priorSettled, err := args.Persist.Nodes().GetMostRecentSettledRun(ctx, tx, row.NodeID, row.RunScopeID, row.Sequence)
	if err != nil {
		return false, fmt.Errorf("prior settled: %w", err)
	}
	if priorSettled == nil {
		return false, nil
	}
	return bagsEqual(ctx, args, tx, priorSettled.RunID, bag)
}

// @concept: cascade
func bagsEqual(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	priorRunID foundationshared.UUID, bag map[string]any,
) (bool, error) {
	prior, err := args.Persist.NodeAttributes().GetDispatchInputBag(ctx, tx, priorRunID)
	if err != nil {
		return false, fmt.Errorf("prior input bag: %w", err)
	}
	if prior == nil {
		return false, nil
	}
	return canonicalEqual(prior, bag)
}

// @concept: cascade
func canonicalEqual(a, b map[string]any) (bool, error) {
	ac, err := canonicalizeBag(a)
	if err != nil {
		return false, err
	}
	bc, err := canonicalizeBag(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ac, bc), nil
}

func canonicalizeBag(m map[string]any) ([]byte, error) {
	return canonical.CanonicalBytes(m)
}
