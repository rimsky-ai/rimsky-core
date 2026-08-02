---
decision: acquire-prefix-fallback
---

# Generic acquire keys still catch classified failures

## Choice

Policy lookup for acquisition failures falls back from the exact producer-declared class to the `acquire/*` family before the unknown-class default; the fallback is documented at the lookup site (see `concept:error-policy`).

## Rationale

An operator declaring only the generic policy should not silently lose coverage the moment a producer starts naming classes.

## Alternatives

- Exact-match only, straight to the unknown-class default when no class-specific policy exists — rejected: a producer adding class names silently strips the operator's generic acquire coverage.
- Fall back through every prefix level of the class name — rejected: one family level covers the real case; deeper hierarchy invites policy shadowing nobody declared.
