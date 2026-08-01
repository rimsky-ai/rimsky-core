---
decision: acquire-unavailable-carveout
status: as-is
---

# The acquire-phase error handlers are named carve-outs outside the resolution engine

## Choice

Two acquire-phase error handlers remain outside the unified claim-handle resolution engine: the acquire-unavailable handler and the acquire-producer-error handler. Both are explicitly named carve-outs sharing one downstream policy-application path: abandon partial opens with no row delete, routing via the producer-declared error class else a synthetic fallback class (`acquire/unavailable` and `acquire/producer_error` respectively).

## Rationale

Both handlers' acquisition transactions have already rolled back, so there is no claimant-guarded delete to fold; forcing either into the engine would widen the engine's contract with a verb-only mode, diluting the single audited verb-then-delete promise. The two handlers share this reasoning and share their downstream policy-application code, differing only in which error class and payload fields each reports.

## Alternatives

- Fold both handlers into the resolution engine as a verb-only mode — rejected: widens the engine's contract and dilutes the single audited verb-then-delete promise for a case with nothing to delete.
