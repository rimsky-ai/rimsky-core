---
decision: persistence-driver
status: adopted
---

# persistence-driver

## Choice

Use the SQLite adapter of `concept:persistence-database`, pointed at a state-database artifact under the per-run artifact directory. Do not add an in-memory variant.

## Rationale

The forensic file is the goal, not ephemeral state. The SQLite adapter needs no setup and produces a queryable artifact in a widely-supported format.

## Alternatives

A new in-memory backend (would gut the audit story; introduces a new conformance surface to maintain). An in-memory mode for the SQLite adapter (still guts the audit story; the existing migration advisory-lock is keyed on a file path, and the per-connection database semantics of an in-memory handle would complicate the implementation).
