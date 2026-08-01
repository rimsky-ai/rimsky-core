---
decision: empty-message-as-root-trigger
status: as-is
---

# Empty message as the universal root trigger

## Choice

Every template's declared-types set carries an implicit empty-string type-path entry, seeded at registration. The entry has a null body schema (no fields, no substitution surface). Author-declared empty-string entries are refused as reserved-for-runtime.

## Rationale

Root triggering rides the typed-message path rather than any runtime-synthetic envelope type. The receipt handler stays uniform with no branch named for the empty case; the cascade walker, substitution validator, and dead-letter audit treat the empty type identically with any other declared type.

## Alternatives

- A dedicated wake control endpoint — rejected: re-introduces a parallel non-message wake path.
- A receipt-handler special-case branch on the empty type — rejected: a runtime branch named for one specific case is exactly the asymmetry the implicit declared-types entry removes.
