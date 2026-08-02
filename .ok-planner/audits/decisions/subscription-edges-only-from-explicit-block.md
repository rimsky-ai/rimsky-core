---
audit: subscription-edges-only-from-explicit-block
artifact: decision:subscription-edges-only-from-explicit-block
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:40Z
---

# Subscription edges come only from the explicit subscription block

Supported. `SubscriptionEdgeMap` (`lib/graph/node/subscription_edges.go`) is constructed in exactly one place, `BuildSubscriptionEdges`: every entry it inserts either comes directly from a node's `subscribes:` list, or is the single synthetic per-structural-root entry it injects on `terminal/success` for nodes with no upstream subscribes entry, no upstream attribute-substitution ref, and no message-body ref (self-subscriptions excluded from the disqualification). No other constructor of `SubscriptionEdgeMap` exists in the codebase — `rg` for the type finds it built only in `subscription_edges.go` and consumed (never separately populated) in `lib/runtime/subscription_loaders.go` and `lib/runtime/substitution_context.go`. Substitution refs contribute no edges: `TestBuildSubscriptionEdges_NoImplicitEdgeFromSubstitutionRef`, `..._NoImplicitEdgeFromMessageRef`, and `..._NoImplicitEdgeFromEnvRef` in `lib/graph/node/subscription_edges_test.go` each assert a node referencing another node's attribute, a message body, or an env var gains no matching edge in the map. The structural-root carve-out itself is proven by `TestBuildSubscriptionEdges_StructuralRootInjection` and its attribute-ref and message-ref variants.
