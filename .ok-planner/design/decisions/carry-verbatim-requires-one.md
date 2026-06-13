---
decision: carry-verbatim-requires-one
status: as-is
---

# Carry-verbatim requires exactly one child

## Choice

The carry-verbatim aggregation policy requires exactly one child, enforced at template validation; a delegation that somehow declares multiple children is a template error (see `concept:child-execution`).

## Rationale

Makes the delegation degenerate case a checked invariant instead of an assumption.
