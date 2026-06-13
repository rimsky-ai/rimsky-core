---
decision: entry-absorption-flag
status: as-is
---

# Entry absorption is a flag on the dispatch input

## Choice

Delegation's entry absorption is an entry-absorbed boolean carried on the dispatch-children primitive's input, not a pre-step before dispatch (see `concept:child-execution`).

## Rationale

One primitive, one call site; a pre-step would reintroduce a second dispatch shape.
