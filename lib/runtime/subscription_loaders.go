// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Per-template edge-map runtime caches.
//
// This file holds two in-process caches keyed by template_hash:
//
//   - templateSubscriptionEdges: the inverse subscription-edge map
//     produced at template registration by
//     graph/node/subscription_edges.go::BuildSubscriptionEdges. Consumed
//     by the cascade walker (runtime/runner_terminal.go::cascadeSubscribersStaleInTx)
//     to route invalidations from a transitioning sender to its
//     subscribed receivers.
//   - templateHardDepEdges: the upstream-refresh edge map produced by
//     graph/node/hard_dep_edges.go::BuildHardDepEdges (subscribes:
//     entries with force_upstream_refresh: true). Consumed by
//     runtime/runner_terminal.go::pullForceRefreshUpstreams to
//     proactively stale-mark upstream node-types when a receiver enters
//     the cascade.
//
// Receiver-side attribute substitution does NOT route through this
// file. The substitution context's Deps map is populated by
// runtime/substitution_context.go::BuildAttributeDeps, which reads
// drained wait-set rows for the receiver in the current frame; the
// retired runtime/subscription_loaders.go::resolveSubscribedSenders
// helper (along with the nodes.dependencies column) was removed by the
// 2026-05-20 per-run attribute-keying spec.
//
//	@concept: node-subscription
//	@concept: wait-set
package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
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
// CanonicalSpecHash in lib/graph/template/canonical/); a future test
// that rebuilds the validator (e.g. with a different
// `Capabilities.declared_events` set) and re-registers the same hash
// would observe the originally-cached edges. If that scenario lands,
// add a per-test reset hook or key the cache on
// `(template_hash, validator_revision)`.
var templateSubscriptionEdges sync.Map

// subscriptionEdgesForTemplate returns the cached or freshly-built
// inverse-edge map for the given template_hash. The persistence handle
// + tx are passed so we can fetch the template spec on cache miss.
func subscriptionEdgesForTemplate(
	ctx context.Context, args RunArgs, templateHash string, tx persistence.Tx,
) (*node.SubscriptionEdgeMap, error) {
	if v, ok := templateSubscriptionEdges.Load(templateHash); ok {
		return v.(*node.SubscriptionEdgeMap), nil
	}
	row, err := args.Persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil {
		return nil, fmt.Errorf("subscriptionEdgesForTemplate: get template %s: %w", templateHash, err)
	}
	if row == nil {
		return nil, fmt.Errorf("subscriptionEdgesForTemplate: template %s not found", templateHash)
	}
	subs := node.ExtractSubstitutionRefsFromTemplate(row.Spec)
	msgs := node.ExtractMessageRefsFromTemplate(row.Spec)
	edges, err := node.BuildSubscriptionEdges(row.Spec, subs, msgs)
	if err != nil {
		return nil, fmt.Errorf("subscriptionEdgesForTemplate: build edges for %s: %w", templateHash, err)
	}
	// @constraint: LoadOrStore wins races; both maps are content-equal when two goroutines compute concurrently, so the first stored value is the canonical one.
	actual, _ := templateSubscriptionEdges.LoadOrStore(templateHash, edges)
	return actual.(*node.SubscriptionEdgeMap), nil
}

// templateDeclaredMessageTypes caches the per-template set of message
// type-paths declared in the template's `messages:` registry. Keyed by
// template_hash. Threaded into `attributes.ResolveContext.RegistryDeclaredTypes`
// at every runtime dispatch-time ResolveContext build site so the
// `{{messages.<type>.<field>}}` resolver's dynamic floor — a directive
// naming an undeclared type returns ErrMissingSource — is actually
// armed at runtime, not just in unit tests. Defense-in-depth against
// runtime-interpolated directives that bypass the registration-time
// validator. Lifetime + cache discipline mirror
// `templateSubscriptionEdges` above.
//
//	@concept: message-schema
var templateDeclaredMessageTypes sync.Map

// declaredMessageTypesForTemplate returns the cached or freshly-built
// set of declared message type-paths for the given template_hash.
// Returns nil on a missing template or persistence error — callers
// thread the nil through into ResolveContext.RegistryDeclaredTypes
// (which treats nil as "no floor", same as if the field were absent).
// This keeps the dispatch-time ResolveContext build cheap (no error
// propagation) while still arming the floor on the happy path; a
// template-missing condition is already surfaced loudly by every other
// dispatch-time helper.
//
// Every template's declared-types set carries an implicit `""` entry
// seeded here at first build, so empty-typed messages pass the
// receipt-time registry-membership check uniformly with any declared
// type. The implicit entry has a null body schema (no fields, no
// substitution surface); receivers gate on the entry via subscription
// edges, not via body substitution. The template_validator refuses an
// author-declared `messages:` entry of type `""` as reserved-for-runtime.
//
//	@decision: empty-message-as-root-trigger
//	@story: empty-message-wakes-roots
func declaredMessageTypesForTemplate(
	ctx context.Context, args RunArgs, templateHash string, tx persistence.Tx,
) map[string]struct{} {
	if templateHash == "" {
		return nil
	}
	if v, ok := templateDeclaredMessageTypes.Load(templateHash); ok {
		return v.(map[string]struct{})
	}
	if args.Persist == nil || args.Persist.Templates() == nil {
		return nil
	}
	row, err := args.Persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil || row == nil {
		return nil
	}
	set := make(map[string]struct{}, len(row.Spec.Messages)+1)
	for _, m := range row.Spec.Messages {
		set[m.Type] = struct{}{}
	}
	// @deliberate: implicit empty-message-type seed — per
	// decision:empty-message-as-root-trigger, every template admits the
	// `""` type so the empty-message wake trigger passes the receipt-time
	// registry-membership check uniformly with any declared type.
	set[""] = struct{}{}
	actual, _ := templateDeclaredMessageTypes.LoadOrStore(templateHash, set)
	return actual.(map[string]struct{})
}

// templateHardDepEdges caches the per-template upstream-refresh edge
// map (computed from subscribes: entries with force_upstream_refresh:
// true) so the cascade walker can look up upstream-refresh edges
// without re-parsing the template spec on every walk.
//
//	@concept: cascade
//	@concept: node-subscription
var templateHardDepEdges sync.Map

// hardDepEdgesForTemplate returns the cached or freshly-built
// hard-dep edge map for templateHash. Returns an error if the
// template's hard-dep edge graph contains a cycle (caught at
// registration; surfaced here as a defensive re-check).
func hardDepEdgesForTemplate(
	ctx context.Context, args RunArgs, templateHash string, tx persistence.Tx,
) (node.HardDepEdgeMap, error) {
	if v, ok := templateHardDepEdges.Load(templateHash); ok {
		return v.(node.HardDepEdgeMap), nil
	}
	tmpl, err := args.Persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil {
		return nil, fmt.Errorf("hardDepEdgesForTemplate: get template %s: %w", templateHash, err)
	}
	if tmpl == nil {
		return nil, fmt.Errorf("hardDepEdgesForTemplate: template %s not found", templateHash)
	}
	edges, err := node.BuildHardDepEdges(tmpl.Spec)
	if err != nil {
		return nil, err
	}
	actual, _ := templateHardDepEdges.LoadOrStore(templateHash, edges)
	return actual.(node.HardDepEdgeMap), nil
}
