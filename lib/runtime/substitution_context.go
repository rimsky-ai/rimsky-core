// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: attribute
// @concept: node-run
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @concept: attribute
// @decision: substitution-grammar-closed
// @decision: substitution-deps-from-persisted-senders
func BuildAttributeDeps(
	ctx context.Context, args RunArgs, receiverNodeRunID foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx,
) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage)

	if receiverNodeRunID != (foundationshared.UUID{}) {
		if err := populateSubscribedSenderDeps(ctx, args, receiverNodeRunID, out, tx); err != nil {
			return nil, err
		}
	}

	// @concept: message
	// @concept: message-schema
	if args.Persist != nil && args.Persist.Messages() != nil {
		msgs, msgErr := args.Persist.Messages().ListDeliveredForFrame(ctx, frameID, tx)
		if msgErr != nil {
			return nil, fmt.Errorf("BuildAttributeDeps: list delivered messages: %w", msgErr)
		}
		for _, m := range msgs {
			if m.IsEmptyWake() {
				continue
			}
			if _, alreadyPresent := out[m.Type]; alreadyPresent {
				continue
			}
			if len(m.Payload) == 0 {
				out[m.Type] = json.RawMessage(`{}`)
				continue
			}
			out[m.Type] = m.Payload
		}
	}

	return out, nil
}

// @concept: attribute
// @concept: cascade
// @story: sequenced-preserves-cascade-rounds
func populateSubscribedSenderDeps(
	ctx context.Context, args RunArgs, receiverNodeRunID foundationshared.UUID, out map[string]json.RawMessage, tx persistence.Tx,
) error {
	rec, err := args.Persist.Nodes().GetRunForGate(ctx, receiverNodeRunID, tx)
	if err != nil {
		return fmt.Errorf("populateSubscribedSenderDeps: get receiver run: %w", err)
	}
	if rec == nil {
		return nil
	}
	receiverNode, err := args.Persist.Nodes().Get(ctx, rec.NodeID, tx)
	if err != nil {
		return fmt.Errorf("populateSubscribedSenderDeps: get receiver node: %w", err)
	}
	if receiverNode == nil {
		return nil
	}
	inst, err := args.Persist.Instances().Get(ctx, receiverNode.InstanceID, tx)
	if err != nil {
		return fmt.Errorf("populateSubscribedSenderDeps: get instance: %w", err)
	}
	if inst == nil {
		return nil
	}
	edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
	if err != nil {
		return fmt.Errorf("populateSubscribedSenderDeps: subscription edges: %w", err)
	}
	if edges == nil {
		return nil
	}
	senderTypes := edges.SenderNodeTypesForReceiver(receiverNode.NodeType)
	if len(senderTypes) == 0 {
		return nil
	}
	senderTypeSet := make(map[string]struct{}, len(senderTypes))
	for _, t := range senderTypes {
		senderTypeSet[t] = struct{}{}
	}
	if err := populateEntryAliasDepsViaCallingNode(ctx, args, rec, edges, receiverNode.NodeType, out, tx); err != nil {
		return err
	}
	pinned, err := pinnedSenderRunsForReceiver(ctx, args, rec.FrameID, receiverNodeRunID, tx)
	if err != nil {
		return err
	}
	instNodes, err := args.Persist.Nodes().ListByInstance(ctx, receiverNode.InstanceID, tx)
	if err != nil {
		return fmt.Errorf("populateSubscribedSenderDeps: list instance nodes: %w", err)
	}
	for _, n := range instNodes {
		if n.ID == rec.NodeID {
			continue
		}
		if _, ok := senderTypeSet[n.NodeType]; !ok {
			continue
		}
		sourceRunID, pinnedRound := pinned[n.ID]
		if !pinnedRound {
			latest, latestErr := args.Persist.Nodes().GetMostRecentSettledRun(ctx, n.ID, rec.RunScopeID, math.MaxInt64, tx)
			if latestErr != nil {
				return fmt.Errorf("populateSubscribedSenderDeps: latest fresh run for %s: %w", n.NodeType, latestErr)
			}
			if latest == nil {
				continue
			}
			sourceRunID = latest.NodeRunID
		}
		raw, rawErr := senderAttrsRaw(ctx, args, sourceRunID, n.NodeType, tx)
		if rawErr != nil {
			return rawErr
		}
		out[n.NodeType] = raw
	}
	return nil
}

// @concept: attribute
// @concept: node-run
func populateEntryAliasDepsViaCallingNode(
	ctx context.Context, args RunArgs, rec *persistence.NodeRunForGate, edges *node.SubscriptionEdgeMap, receiverNodeType string, out map[string]json.RawMessage, tx persistence.Tx,
) error {
	aliasSenders := edges.CallingNodeSenderTypesForReceiver(receiverNodeType)
	if len(aliasSenders) == 0 {
		return nil
	}
	scope, err := args.Persist.RunScopes().GetByID(ctx, rec.RunScopeID, tx)
	if err != nil {
		return fmt.Errorf("populateEntryAliasDepsViaCallingNode: load run scope: %w", err)
	}
	if scope == nil || scope.ParentNodeRunID == nil {
		return nil
	}
	callerRun, err := args.Persist.NodeRunTree().GetByID(ctx, *scope.ParentNodeRunID, tx)
	if err != nil {
		return fmt.Errorf("populateEntryAliasDepsViaCallingNode: load calling node run: %w", err)
	}
	if callerRun == nil {
		return nil
	}
	for _, alias := range aliasSenders {
		raw, err := senderAttrsRaw(ctx, args, callerRun.NodeRunID, alias, tx)
		if err != nil {
			return err
		}
		out[alias] = raw
	}
	return nil
}

// @concept: wait-set
// @story: sequenced-preserves-cascade-rounds
func pinnedSenderRunsForReceiver(
	ctx context.Context, args RunArgs, frameID, receiverNodeRunID foundationshared.UUID, tx persistence.Tx,
) (map[foundationshared.UUID]foundationshared.UUID, error) {
	rows, err := args.Persist.WaitSet().ListForReceiver(ctx, frameID, receiverNodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("pinnedSenderRunsForReceiver: list wait-set: %w", err)
	}
	pinnedByNode := make(map[foundationshared.UUID]foundationshared.UUID)
	seqByNode := make(map[foundationshared.UUID]int64)
	seenRun := make(map[foundationshared.UUID]struct{})
	for _, w := range rows {
		if _, done := seenRun[w.SenderNodeRunID]; done {
			continue
		}
		seenRun[w.SenderNodeRunID] = struct{}{}
		senderRun, runErr := args.Persist.Nodes().GetRunForGate(ctx, w.SenderNodeRunID, tx)
		if runErr != nil {
			return nil, fmt.Errorf("pinnedSenderRunsForReceiver: get sender run: %w", runErr)
		}
		if senderRun == nil {
			continue
		}
		if cur, ok := seqByNode[senderRun.NodeID]; !ok || senderRun.Sequence > cur {
			pinnedByNode[senderRun.NodeID] = w.SenderNodeRunID
			seqByNode[senderRun.NodeID] = senderRun.Sequence
		}
	}
	return pinnedByNode, nil
}

func senderAttrsRaw(
	ctx context.Context, args RunArgs, runID foundationshared.UUID, nodeType string, tx persistence.Tx,
) (json.RawMessage, error) {
	attrRow, err := args.Persist.NodeAttributes().GetByRun(ctx, runID, tx)
	if err != nil {
		return nil, fmt.Errorf("populateSubscribedSenderDeps: attribute row for %s: %w", nodeType, err)
	}
	if attrRow == nil {
		return json.RawMessage(`{}`), nil
	}
	marshaled, err := json.Marshal(attrRow.Data)
	if err != nil {
		return nil, fmt.Errorf("populateSubscribedSenderDeps: marshal attrs for %s: %w", nodeType, err)
	}
	return marshaled, nil
}

func buildResolveContextForAcquisition(
	ctx context.Context, args RunArgs, frameID, receiverNodeRunID foundationshared.UUID, templateHash string, params json.RawMessage, claims map[string]claimproducer.ClaimResult, tx persistence.Tx,
) (attributes.ResolveContext, error) {
	deps, err := BuildAttributeDeps(ctx, args, receiverNodeRunID, frameID, tx)
	if err != nil {
		return attributes.ResolveContext{}, fmt.Errorf("buildResolveContextForAcquisition: %w", err)
	}
	return attributes.ResolveContext{
		Deps:                  deps,
		Claim:                 claims,
		Params:                params,
		RegistryDeclaredTypes: declaredMessageTypesForTemplate(ctx, args, templateHash, tx),
	}, nil
}
