---
decision: migrations-append-only-numbered
status: as-is
---

# Migration discipline

## Choice

Schema migrations are numerically ordered and append-only, maintained per backend (see `concept:persistence-database`).

## Rationale

The migration runner's contract is a totally ordered, immutable sequence: numbering gives the order, append-only keeps every database's applied prefix valid forever. Pre-v1 schema rethinks are expressed as new drop-and-recreate migrations, never by editing history (see `decision:migrations-no-compat-shims`).

## Alternatives

- An external migration framework — rejected: a dependency for a job a small ordered per-backend runner already covers.
- Editable or rebased migrations pre-v1 — rejected: breaks the runner's applied-prefix contract on any database that already ran the old sequence.
