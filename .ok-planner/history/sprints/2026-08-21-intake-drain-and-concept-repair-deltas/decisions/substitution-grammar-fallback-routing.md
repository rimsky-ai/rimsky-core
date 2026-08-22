---
decision: substitution-grammar-fallback-routing
---

# Substitution-grammar fallback routing

## Choice

Unresolved substitution refs route through the fallback / lenient / optional handling at dispatch whenever a ref's source value is absent — whether because the sender was out of scope, or because the sender has no fresh-settled run to read from (per `decision:substitution-deps-from-persisted-senders`). Authors declare literal fallbacks or lenient (optional) markers in the substitution grammar. A directive admits at most one literal fallback. The grammar rejects chains across several directives and composite literals, and a directive carries either the lenient marker or a literal fallback, never both.

## Rationale

A single graceful-degradation grammar covers every uncovered-ref case under the author's explicit control — no-scope and no-settled-run conditions share one resolution path, so authors learn one rule for absence rather than two.

One fallback per directive keeps absence handling readable at the directive: the reader sees the source, then the one thing that happens when it is absent. A chain would make the resolved value depend on the evaluation order of several sources. The lenient marker and the literal fallback answer the same question with different answers — null on missing against a literal on missing — so a directive carrying both would have no single meaning.

## Alternatives

- Distinct handling per absence cause (no-scope distinct from no-settled-run) — rejected: two rules for one authoring question, forcing the author to predict which absence a given ref will hit.
- Hard-fail every unresolved ref at dispatch — rejected: removes the author's ability to declare graceful degradation for legitimately absent sources.
- Multi-directive fallback chains — rejected: the resolved value follows the evaluation order of several sources rather than reading off the directive.
- The lenient marker and a literal fallback together on one directive — rejected: the two prescribe different values for the same missing source.
