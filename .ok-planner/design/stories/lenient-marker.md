---
story: lenient-marker
status: as-is
---

# Template author marks substitution lenient

## Role

As a template author, I can mark a substitution directive lenient with the lenient marker so a missing source resolves to empty at runtime instead of failing dispatch, so that I can write templates that gracefully handle optional upstream inputs.

## Capability

Lenient marker on substitution directives: missing source resolves to empty at runtime; absence of the marker keeps the strict (fail-on-missing) behavior.

## Business value

Template authors gracefully handle optional upstream inputs without writing handler branches; the strict / lenient distinction is explicit at the directive site.

