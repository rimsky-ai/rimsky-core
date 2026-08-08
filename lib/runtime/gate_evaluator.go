// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: cascade
// @decision: mode-default-most-recent
func evaluateGatesAfterDrain(
	ctx context.Context, args RunArgs, frameID, senderNodeRunID foundationshared.UUID, tx persistence.Tx,
) error {
	receiverIDs, err := args.Persist.WaitSet().ListPendingReceiversForDrainedSender(ctx, frameID, senderNodeRunID, tx)
	if err != nil {
		return fmt.Errorf("evaluateGatesAfterDrain: list receivers: %w", err)
	}
	for _, receiverID := range receiverIDs {
		if err := evaluateOneGate(ctx, args, receiverID, tx); err != nil {
			return fmt.Errorf("evaluateGatesAfterDrain: receiver %s: %w", receiverID, err)
		}
	}
	return nil
}

// @concept: cascade
// @concept: cascade-mode
// @decision: mode-default-most-recent
func evaluateOneGate(
	ctx context.Context, args RunArgs, receiverNodeRunID foundationshared.UUID, tx persistence.Tx,
) error {
	row, err := args.Persist.Nodes().GetRunForGate(ctx, receiverNodeRunID, tx)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if row == nil || row.State != cascade.NodeStatePending {
		return nil
	}
	undrained, err := args.Persist.WaitSet().HasUndrainedRowsForReceiver(ctx, row.FrameID, row.NodeRunID, tx)
	if err != nil {
		return fmt.Errorf("undrained probe: %w", err)
	}
	if undrained {
		return nil
	}
	upstreamInFlight, err := anySubscribedUpstreamInFlight(ctx, args, row, tx)
	if err != nil {
		return fmt.Errorf("upstream probe: %w", err)
	}
	if upstreamInFlight {
		return nil
	}
	carryForward, err := loadReceiverCarryForward(ctx, args, row, tx)
	if err != nil {
		return fmt.Errorf("carry-forward: %w", err)
	}
	resolved, err := buildResolvedBagAtGateEvalCarry(ctx, args, row, carryForward, tx)
	if err != nil {
		if isSubstitutionClassifiableError(err) {
			return routeSubstitutionFailureAtGate(ctx, args, row, err, tx)
		}
		return fmt.Errorf("resolve at gate-eval: %w", err)
	}
	mode, err := args.Persist.Nodes().GetCascadeMode(ctx, row.NodeID, tx)
	if err != nil {
		return fmt.Errorf("get cascade mode: %w", err)
	}
	drop, err := applyCascadeModeRule(ctx, args, row, resolved, mode, tx)
	if err != nil {
		return fmt.Errorf("mode rule: %w", err)
	}
	if drop {
		return args.Persist.Nodes().DropPendingRun(ctx, row.NodeRunID, tx)
	}
	advancedSibling, err := args.Persist.Nodes().HasAdvancedSiblingInScope(ctx, row.NodeID, row.RunScopeID, row.NodeRunID, tx)
	if err != nil {
		return fmt.Errorf("advanced sibling probe: %w", err)
	}
	if advancedSibling {
		return nil
	}
	if err := args.Persist.NodeAttributes().Upsert(ctx, row.NodeRunID, row.NodeID, resolved, tx); err != nil {
		return fmt.Errorf("seed live bag: %w", err)
	}
	if err := args.Persist.NodeAttributes().SetDispatchInputBag(ctx, row.NodeRunID, row.NodeID, resolved, tx); err != nil {
		return fmt.Errorf("persist dispatch bag: %w", err)
	}
	return args.Persist.Nodes().TransitionPendingToStale(ctx, row.NodeRunID, args.Clock.Now(), tx)
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
	ctx context.Context, args RunArgs, row *persistence.NodeRunForGate, subErr error, tx persistence.Tx,
) error {
	receiverNode, err := args.Persist.Nodes().Get(ctx, row.NodeID, tx)
	if err != nil {
		return fmt.Errorf("route substitution failure: load receiver node: %w", err)
	}
	if receiverNode == nil {
		return nil
	}
	tmplSpec, err := loadTemplateSpec(ctx, args, receiverNode.InstanceID, tx)
	if err != nil {
		return fmt.Errorf("route substitution failure: load template: %w", err)
	}
	var nodeDef *node.TemplateNodeDef
	if tmplSpec != nil {
		nodeDef = LookupNodeDef(tmplSpec, receiverNode.NodeType)
	}
	class, eventKind := classifyAttributeFailure(subErr)
	directive := extractDirective(subErr)
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID:     &row.NodeID,
		InstanceID: &receiverNode.InstanceID,
		Kind:       eventKind,
		Payload: eventpayload.New(&genv1.TemplateResolutionFailedPayload{
			Directive: directive,
			Site:      substitutionSiteAttribute,
			Field:     attributes.FieldOf(subErr),
			Reason:    subErr.Error(),
		}),
	}, tx); err != nil {
		return fmt.Errorf("route substitution failure: append event: %w", err)
	}
	if err := args.Persist.Nodes().TransitionPendingToStale(ctx, row.NodeRunID, args.Clock.Now(), tx); err != nil {
		return fmt.Errorf("route substitution failure: pending->stale: %w", err)
	}
	acq := &acquisition{
		NodeRunID:  row.NodeRunID,
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
	ctx context.Context, args RunArgs, row *persistence.NodeRunForGate, carryForward map[string]any, tx persistence.Tx,
) (map[string]any, error) {
	receiverNode, err := args.Persist.Nodes().Get(ctx, row.NodeID, tx)
	if err != nil || receiverNode == nil {
		if err != nil {
			return nil, err
		}
		return carryForward, nil
	}
	tmplSpec, err := loadTemplateSpec(ctx, args, receiverNode.InstanceID, tx)
	if err != nil {
		return nil, err
	}
	if tmplSpec == nil {
		return carryForward, nil
	}
	nodeDef := LookupNodeDef(tmplSpec, receiverNode.NodeType)
	templateDefaults := templateAttributeDefaultsFor(tmplSpec, receiverNode.Executor)
	schema := schemaForGateEval(args, receiverNode.Executor, templateDefaults, nodeDef)
	if schema == nil {
		return map[string]any{}, nil
	}
	deps, err := BuildAttributeDeps(ctx, args, row.NodeRunID, row.FrameID, tx)
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
func schemaForGateEval(args RunArgs, executor string, templateDefaults map[string]any, nodeDef *node.TemplateNodeDef) map[string]any {
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
	return node.MergeAttributeDefaults(execSchema, templateDefaults, nodeSchema)
}

// @concept: cascade
// @decision: held-as-state-not-phase
// @decision: upstream-gating-at-eligibility
func anySubscribedUpstreamInFlight(
	ctx context.Context, args RunArgs, row *persistence.NodeRunForGate, tx persistence.Tx,
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
	tmplSpec, err := loadTemplateSpec(ctx, args, receiverNode.InstanceID, tx)
	if err != nil {
		return false, err
	}
	coMembers := subgraphMembersIncludingType(tmplSpec, receiverNode.NodeType)
	phases, err := args.Queue.ListInFlightRunStates(ctx, senderIDs, row.FrameID, row.RunScopeID, tx)
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
func loadReceiverCarryForward(
	ctx context.Context, args RunArgs, row *persistence.NodeRunForGate, tx persistence.Tx,
) (map[string]any, error) {
	prior, err := args.Persist.Nodes().GetPriorRunBySequence(ctx, row.NodeID, row.RunScopeID, row.Sequence, tx)
	if err != nil {
		return nil, fmt.Errorf("prior run: %w", err)
	}
	if prior == nil {
		return map[string]any{}, nil
	}
	priorBag, err := args.Persist.NodeAttributes().GetByRun(ctx, prior.NodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("prior attrs: %w", err)
	}
	if priorBag == nil || len(priorBag.Data) == 0 {
		return map[string]any{}, nil
	}
	buf, err := json.Marshal(priorBag.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal prior bag for deep copy: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, fmt.Errorf("unmarshal prior bag for deep copy: %w", err)
	}
	return out, nil
}

// @concept: cascade
// @concept: cascade-mode
// @decision: mode-default-most-recent
func applyCascadeModeRule(
	ctx context.Context, args RunArgs, row *persistence.NodeRunForGate, bag map[string]any, mode spec.CascadeMode, tx persistence.Tx,
) (drop bool, err error) {
	switch mode {
	case spec.CascadeModeMostRecent, "":
		hasLater, herr := args.Persist.Nodes().HasLaterCascadePending(ctx, row.NodeID, row.RunScopeID, row.Sequence, tx)
		if herr != nil {
			return false, fmt.Errorf("most-recent: has later: %w", herr)
		}
		if hasLater {
			return true, nil
		}
		if _, derr := args.Persist.Nodes().DeletePriorCascadeStales(ctx, row.NodeID, row.RunScopeID, row.Sequence, tx); derr != nil {
			return false, fmt.Errorf("most-recent: delete prior: %w", derr)
		}
		return false, nil
	case spec.CascadeModeSequenced:
		return false, nil
	case spec.CascadeModeIdempotentQueue:
		return modeDropIfPriorEqual(ctx, args, row, bag, false, tx)
	case spec.CascadeModeIdempotentSettled:
		return modeDropIfPriorEqual(ctx, args, row, bag, true, tx)
	}
	return false, fmt.Errorf("applyCascadeModeRule: unknown mode %q", mode)
}

// @concept: cascade
func modeDropIfPriorEqual(
	ctx context.Context, args RunArgs, row *persistence.NodeRunForGate, bag map[string]any, includeSettled bool, tx persistence.Tx,
) (bool, error) {
	priorQueued, err := args.Persist.Nodes().GetPriorCascadeQueuedNotClaimed(ctx, row.NodeID, row.RunScopeID, row.Sequence, tx)
	if err != nil {
		return false, fmt.Errorf("prior queued: %w", err)
	}
	if priorQueued != nil {
		return bagsEqual(ctx, args, priorQueued.NodeRunID, bag, tx)
	}
	if !includeSettled {
		return false, nil
	}
	priorSettled, err := args.Persist.Nodes().GetMostRecentSettledRun(ctx, row.NodeID, row.RunScopeID, row.Sequence, tx)
	if err != nil {
		return false, fmt.Errorf("prior settled: %w", err)
	}
	if priorSettled == nil {
		return false, nil
	}
	return bagsEqual(ctx, args, priorSettled.NodeRunID, bag, tx)
}

// @concept: cascade
func bagsEqual(
	ctx context.Context, args RunArgs, priorRunID foundationshared.UUID, bag map[string]any, tx persistence.Tx,
) (bool, error) {
	prior, err := args.Persist.NodeAttributes().GetDispatchInputBag(ctx, priorRunID, tx)
	if err != nil {
		return false, fmt.Errorf("prior input bag: %w", err)
	}
	if prior == nil {
		priorRow, gerr := args.Persist.Nodes().GetRunForGate(ctx, priorRunID, tx)
		if gerr != nil {
			return false, fmt.Errorf("prior row for on-demand resolve: %w", gerr)
		}
		if priorRow == nil {
			return false, nil
		}
		carry, cerr := loadReceiverCarryForward(ctx, args, priorRow, tx)
		if cerr != nil {
			return false, fmt.Errorf("prior carry: %w", cerr)
		}
		resolved, rerr := buildResolvedBagAtGateEvalCarry(ctx, args, priorRow, carry, tx)
		if rerr != nil {
			return false, fmt.Errorf("prior on-demand resolve: %w", rerr)
		}
		prior = resolved
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
