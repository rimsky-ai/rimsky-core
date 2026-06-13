---
decision: acquire-unavailable-carveout
status: as-is
---

# The acquire-unavailable handler is the single named carve-out

## Choice

The acquire-unavailable handler remains outside the unified claim-handle resolution engine, explicitly named as the single carve-out, with a deterministic injection test (see `decision:race-injection-hooks`) pinning its behavior: abandon partial opens, no row delete (the acquisition transaction's rows already rolled back), route via the producer-declared class else the synthetic `acquire/unavailable` class.

## Rationale

Its acquisition transaction has already rolled back, so there is no claimant-guarded delete to fold; forcing it into the engine would widen the engine's contract with a verb-only mode, diluting the single audited verb-then-delete promise.
