---
decision: acquire-prefix-fallback
status: as-is
---

# Generic acquire keys still catch classified failures

## Choice

Policy lookup for acquisition failures falls back from the exact producer-declared class to the `acquire/*` family before the unknown-class default; the fallback is documented at the lookup site (see `concept:error-policy`).

## Rationale

An operator declaring only the generic policy should not silently lose coverage the moment a producer starts naming classes.
