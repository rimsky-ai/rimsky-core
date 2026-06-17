---
decision: empty-message-as-root-trigger
status: as-is
---

# Empty message as the universal root trigger

## Choice

Every template's declared-types set carries an implicit empty-string type-path entry, seeded at registration. The entry has a null body schema (no fields, no substitution surface). Author-declared empty-string entries are refused as reserved-for-runtime.

## Rationale

Collapses the prior runtime-synthetic envelope types onto the typed-message path. The receipt handler stays uniform with no branch named for the empty case; the cascade walker, substitution validator, and dead-letter audit treat the empty type identically with any other declared type.

## Alternatives considered

A dedicated wake control endpoint — would re-introduce a parallel non-message wake path; a receipt-handler special-case branch on the empty type — the runtime branch named for a specific case is the asymmetry the spec is otherwise trying to retire.
