// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Subscription-edge runtime cache + receiver-side resolution helpers.
// The per-template subscription-edge inverse map is computed by
// graph/node/subscription_edges.go::BuildSubscriptionEdges at template
// registration. Runtime fetches that map lazily on first dispatch for
// a template_hash and caches the result in-process. Receivers consume
// the cached map through resolveSubscribedSenders to populate the
// substitution context's Deps map without reading the retired
// nodes.dependencies column.
//
//	@concept: node-subscription
//	@concept: wait-set
package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/node"
)

// templateSubscriptionEdges caches the per-template inverse
// subscription-edge map. Keyed by template_hash; populated lazily on
// first lookup. Edges are immutable per template_hash by construction
// (templates are content-addressed), so the cache never invalidates
// — a hash collision would imply a content-equal template.
//
// Lifetime: process-global. Across test runs in the same process, the
// cache persists. This is safe today because template_hash is
// deterministic over canonical bytes (see
// `code:graph/template/canonical/CanonicalSpecHash`); a future test
// that rebuilds the validator (e.g. with a different
// `Capabilities.declared_events` set) and re-registers the same hash
// would observe the originally-cached edges. If that scenario lands,
// add a per-test reset hook or key the cache on
// `(template_hash, validator_revision)`.
var templateSubscriptionEdges sync.Map // map[string]node.SubscriptionEdgeMap

// subscriptionEdgesForTemplate returns the cached or freshly-built
// inverse-edge map for the given template_hash. The persistence handle
// + tx are passed so we can fetch the template spec on cache miss.
func subscriptionEdgesForTemplate(
	ctx context.Context, args RunArgs, templateHash string, tx persistence.Tx,
) (node.SubscriptionEdgeMap, error) {
	if v, ok := templateSubscriptionEdges.Load(templateHash); ok {
		return v.(node.SubscriptionEdgeMap), nil
	}
	row, err := args.Persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil {
		return nil, fmt.Errorf("subscriptionEdgesForTemplate: get template %s: %w", templateHash, err)
	}
	if row == nil {
		return nil, fmt.Errorf("subscriptionEdgesForTemplate: template %s not found", templateHash)
	}
	subs := node.ExtractSubstitutionRefsFromTemplate(row.Spec)
	edges := node.BuildSubscriptionEdges(row.Spec, subs)
	// LoadOrStore wins races: if two goroutines compute concurrently,
	// both maps are content-equal; we keep the first.
	actual, _ := templateSubscriptionEdges.LoadOrStore(templateHash, edges)
	return actual.(node.SubscriptionEdgeMap), nil
}

// resolveSubscribedSenders returns the set of sender node-IDs that the
// receiver receiverNodeID subscribes to (either explicitly via
// `subscribes:` or implicitly via substitution refs in the receiver's
// attribute schema). Used to populate the Deps map for substitution at
// dispatch and to drive the cascade walk under the post-2026-05-14
// subscription model — replaces reads of the retired
// nodes.dependencies column.
//
// Returns nil for receivers with no subscription edges (no upstream
// gating).
func resolveSubscribedSenders(
	ctx context.Context, args RunArgs, receiverNodeID shared.UUID, tx persistence.Tx,
) ([]shared.UUID, error) {
	receiver, err := args.Persist.Nodes().Get(ctx, receiverNodeID, tx)
	if err != nil {
		return nil, fmt.Errorf("resolveSubscribedSenders: get receiver %s: %w", receiverNodeID, err)
	}
	if receiver == nil {
		return nil, nil
	}
	inst, err := args.Persist.Instances().Get(ctx, receiver.InstanceID, tx)
	if err != nil {
		return nil, fmt.Errorf("resolveSubscribedSenders: get instance %s: %w", receiver.InstanceID, err)
	}
	if inst == nil {
		return nil, nil
	}
	edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
	if err != nil {
		return nil, err
	}
	// Collect the sender node-types this receiver listens to.
	//
	// Cross-cutting (`instance: true`) edges are skipped here: they
	// have no single sender node-type to enumerate (they fire on every
	// node in the instance matching the topic filter at observation
	// time). The runtime cascade walk routes cross-cutting fan-out
	// directly via the empty-key bucket in the inverse-edge map (see
	// `cascadeSubscribersStaleInTx`); this loader serves the
	// per-receiver Deps-map case where the receiver must read concrete
	// upstream attribute values, which cross-cutting edges don't
	// provide.
	senderTypes := make(map[string]struct{})
	for senderType, list := range edges {
		if senderType == "" {
			continue
		}
		for _, e := range list {
			if e.ReceiverNodeType == receiver.NodeType {
				senderTypes[senderType] = struct{}{}
				break
			}
		}
	}
	if len(senderTypes) == 0 {
		return nil, nil
	}
	// Map sender node-types back to node IDs within the receiver's instance.
	instNodes, err := args.Persist.Nodes().ListByInstance(ctx, receiver.InstanceID, tx)
	if err != nil {
		return nil, fmt.Errorf("resolveSubscribedSenders: list instance nodes: %w", err)
	}
	var out []shared.UUID
	for _, n := range instNodes {
		if _, ok := senderTypes[n.NodeType]; ok {
			out = append(out, n.ID)
		}
	}
	return out, nil
}
