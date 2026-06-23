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
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @concept: attribute
// @decision: substitution-grammar-closed
func BuildAttributeDeps(
	ctx context.Context,
	tx persistence.Tx,
	args RunArgs,
	receiverRunID foundationshared.UUID,
	frameID foundationshared.UUID,
) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage)

	if receiverRunID != (foundationshared.UUID{}) {
		if err := populateSubscribedSenderDeps(ctx, tx, args, receiverRunID, out); err != nil {
			return nil, err
		}
	}

	// @concept: message
	// @concept: message-schema
	if args.Persist != nil && args.Persist.Messages() != nil {
		msgs, msgErr := args.Persist.Messages().ListDeliveredForFrame(ctx, tx, frameID)
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
func populateSubscribedSenderDeps(
	ctx context.Context, tx persistence.Tx, args RunArgs,
	receiverRunID foundationshared.UUID, out map[string]json.RawMessage,
) error {
	rec, err := args.Persist.Nodes().GetRunForGate(ctx, tx, receiverRunID)
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
		latest, err := args.Persist.Nodes().GetMostRecentSettledRun(ctx, tx, n.ID, rec.RunScopeID, math.MaxInt64)
		if err != nil {
			return fmt.Errorf("populateSubscribedSenderDeps: latest fresh run for %s: %w", n.NodeType, err)
		}
		if latest == nil {
			continue
		}
		attrRow, err := args.Persist.NodeAttributes().GetByRun(ctx, latest.RunID, tx)
		if err != nil {
			return fmt.Errorf("populateSubscribedSenderDeps: attribute row for %s: %w", n.NodeType, err)
		}
		var raw json.RawMessage
		if attrRow == nil {
			raw = json.RawMessage(`{}`)
		} else {
			marshaled, marshalErr := json.Marshal(attrRow.Data)
			if marshalErr != nil {
				return fmt.Errorf("populateSubscribedSenderDeps: marshal attrs for %s: %w", n.NodeType, marshalErr)
			}
			raw = marshaled
		}
		out[n.NodeType] = raw
	}
	return nil
}

func buildResolveContextForAcquisition(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	frameID, receiverRunID foundationshared.UUID,
	templateHash string,
	params json.RawMessage,
	claims map[string]claimproducer.ClaimResult,
) (attributes.ResolveContext, error) {
	deps, err := BuildAttributeDeps(ctx, tx, args, receiverRunID, frameID)
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
