---
audit: structural-root-edge-injection-at-registration
artifact: decision:structural-root-edge-injection-at-registration
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:33:36Z
---

# Synthetic wake edges for structural roots are injected into the derived edge map at template registration

Unsupported. The mechanism exists and behaves as described in its essentials: the edge builder inserts one synthetic entry per qualifying node under an empty sender key, waking on a success terminal without forcing upstream refresh; the disqualification test is exactly the three the decision names (a non-self subscribes entry, an upstream attribute substitution reference, or message-body consumption), self-subscriptions are explicitly skipped, the augmentation exists only on the derived map, the canonical template hash is taken over canonicalized spec bytes and never sees it, and the cascade walker needs no special case because the empty-type trigger node is a real node row materialized at instance creation and matches through the ordinary sender lookup. Two claims do not hold. The injection does not happen at template registration: no registration path builds the edge map at all — it is derived on demand from the stored spec and memoized per template hash, and all three derivation sites (the supervisor's cache, a CLI helper, and the test harness) recompute it independently, which is closer to the deferred computation the decision names as its rejected alternative than to the registration-time act it claims. And the structural-root population is narrower than the definition given: every node type belonging to a non-main graph is skipped before the test is applied, so a subgraph-internal node with no upstream of any kind meets the decision's stated definition and still receives no entry.
