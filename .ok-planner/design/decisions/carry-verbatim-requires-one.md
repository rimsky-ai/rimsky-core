---
decision: carry-verbatim-requires-one
---

# Carry-verbatim requires exactly one child

## Choice

The carry-verbatim aggregation policy requires exactly one child, enforced at template validation; a delegation that somehow declares multiple children is a template error (see `concept:child-execution`).

## Rationale

Makes the delegation degenerate case a checked invariant instead of an assumption.

## Alternatives

- Enforce the single-child shape at settle time instead of template validation — rejected: fails late, after dispatch, on a shape knowable statically.
- Accept multiple children and carry one child's result — rejected: silently discards the rest; "verbatim" would no longer name a guarantee.
