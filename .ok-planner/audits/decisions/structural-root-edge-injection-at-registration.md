---
audit: structural-root-edge-injection-at-registration
artifact: decision:structural-root-edge-injection-at-registration
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:40Z
---

# Structural-root edge injection at template registration

Supported. `BuildSubscriptionEdges` in `lib/graph/node/subscription_edges.go` computes, for every non-subgraph-internal node, whether it has an upstream (a non-self `subscribes` entry, an upstream attribute-substitution ref, or a message-body ref) and, only if none exist, inserts one synthetic edge waking that node on `terminal/success` with `ForceUpstreamRefresh: false`; a self-subscription (`s.Node == def.Type`) is explicitly excluded from counting as an upstream. `lib/runtime/subscription_loaders.go::subscriptionEdgesForTemplate` caches the built map in a process-wide `sync.Map` keyed by template hash, so the augmentation is derived once per template (never per instance) and never stored — matching the "derived map only, per-template" claim. The canonical template hash (`lib/graph/template/canonical/jcs.go::CanonicalSpecHash`) is computed purely from the marshaled, JCS-canonicalized spec bytes and never touches subscription-edge construction, so the augmentation cannot perturb it. Message-ref and attribute-ref suppression of the structural-root injection are each covered by a dedicated unit test (`TestBuildSubscriptionEdges_StructuralRootInjection_AttributeRef`, `TestBuildSubscriptionEdges_MessageRefSuppressesStructuralRoot`); self-subscription non-disqualification is exercised end-to-end by the `inproc-loop-counter` example, whose `counter` node's only subscription is to itself and which nonetheless dispatches (proven by `test/scenarios/inproc_utility_executor_e2e_test.go`, which asserts the loop runs to completion, i.e. the node was seeded as a structural root).
