---
audit: acquire-unavailable-carveout
artifact: decision:acquire-unavailable-carveout
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:47:34Z
---

# Acquire-phase error handlers standing outside the claim-handle resolution engine

Unsupported: the Choice's count is stale. It states that two acquire-phase error handlers remain outside the unified claim-handle resolution engine and describes them as sharing one downstream policy-application path. Enumerating the acquire-phase error handlers from the dispatcher's own error switch gives five, not two: acquire-unavailable, acquire-producer-error, acquire-nil-frame-id, acquire-fan-out-partition-request-substitution-failed, and acquire-lock-spec-substitution-failed. All five stand outside the resolution engine, and all five route through the single shared downstream function — which is where the decision's own annotation sits, so the annotation marks a five-member surface the decision describes as a pair. The rationale narrows no further: it turns on the acquisition transaction having already rolled back so there is nothing claimant-guarded to delete, and that is equally true of the fan-out substitution handler, which likewise abandons its partial opens and deletes no row. The one property that genuinely picks out the named pair is finer than the Choice states: those two, alone among the five, pass a producer-declared error class as the primary lookup key with a synthetic acquire-family class as the fallback, while the other three pass one synthetic class as both. Everything else the Choice asserts about the pair holds — both abandon partial opens, neither deletes a row, both use the two synthetic class names the decision names, and neither is folded into the engine as a verb-only mode.
