---
decision: substitution-grammar-fallback-routing
status: as-is
---

# Substitution-grammar fallback routing

## Choice

Unresolved substitution refs route through the fallback / lenient / optional handling at dispatch whenever a ref's source value is absent — whether because the sender was out of scope, or because the sender has no fresh-settled run to read from (per `decision:substitution-deps-from-persisted-senders`). Authors declare `| "literal"` fallbacks or `?` lenient markers per the substitution-grammar specification.

## Rationale

A single graceful-degradation grammar covers every uncovered-ref case under the author's explicit control — no-scope and no-settled-run conditions share one resolution path, so authors learn one rule for absence rather than two.
