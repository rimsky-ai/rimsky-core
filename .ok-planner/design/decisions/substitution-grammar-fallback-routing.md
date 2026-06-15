---
decision: substitution-grammar-fallback-routing
status: as-is
---

# Substitution-grammar fallback routing

## Choice

Unresolved substitution refs route through the fallback / lenient / optional handling at dispatch whenever a ref's wait-set row is absent — whether because the sender was out of scope, or because the sender settled in the frame before the receiver entered it (the `wake_on_change: false` ordering gap; see `decision:wake-on-change-wait-set-only`). Authors declare `| "literal"` fallbacks or `?` lenient markers per the substitution-grammar specification.

## Rationale

A single graceful-degradation grammar covers every uncovered-ref case under the author's explicit control — no-scope and ordering-gap conditions share one resolution path, so authors learn one rule for absence rather than two.
