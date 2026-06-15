---
decision: migrations-append-only-numbered
status: as-is
---

# Migration discipline

## Choice

Numerically ordered, append-only, per backend (see `concept:persistence-database`).

## Rationale

Migration-runner shape; ordering is the runner's contract.
