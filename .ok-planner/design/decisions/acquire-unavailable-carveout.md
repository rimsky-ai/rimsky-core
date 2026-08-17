---
decision: acquire-unavailable-carveout
---

# The acquire-phase error handlers are named carve-outs outside the resolution engine

## Choice

Five acquire-phase error handlers remain outside the unified claim-handle resolution engine: unavailable, producer error, nil frame id, fan-out substitution failure, and lock-spec substitution failure. All five are explicitly named carve-outs sharing one downstream policy-application path: abandon partial opens with no row delete. The unavailable and producer-error handlers route via the producer-declared error class where the producer supplied one, falling back to a synthetic class (`acquire/unavailable` and `acquire/producer_error` respectively); the other three carry no producer-declared class and route by their own synthetic class alone.

## Rationale

All five handlers run after their acquisition transaction has already rolled back, so there is no claimant-guarded delete to fold; forcing any of them into the engine would widen the engine's contract with a verb-only mode, diluting the single audited verb-then-delete promise. The five share that reasoning and share their downstream policy-application code, differing only in which error class and payload fields each reports.

## Alternatives

- Fold the handlers into the resolution engine as a verb-only mode — rejected: widens the engine's contract and dilutes the single audited verb-then-delete promise for a case with nothing to delete.
