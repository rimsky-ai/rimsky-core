---
decision: persistence-driver
status: adopted
---

# persistence-driver

## Choice

Use the existing sqlite persistence driver, pointed at `<run>/state.db`. Do not add an in-memory variant.

## Rationale

The forensic file is the goal, not ephemeral state. The existing sqlite driver needs no setup and produces a queryable artifact in a widely-supported format.

## Alternatives

A new in-memory backend (would gut the audit story; introduces a new conformance surface to maintain). An in-memory mode for the sqlite driver (still guts the audit story; the existing migration advisory-lock is keyed on a file path, and the per-connection database semantics of an in-memory sqlite handle would complicate the implementation).
