---
decision: claimant-guard-helper
status: as-is
---

# One written claimant-guard predicate per driver

## Choice

Each persistence driver (Postgres, SQLite) routes its claimant-guarded mutations through one internal helper that appends the guard predicate; no hand-written copies of the predicate exist outside the helper (see `concept:claim-handle` — claimant-guarded release).

## Rationale

A predicate written once per driver cannot be inconsistently copied.

## Alternatives

- A cross-driver query builder — rejected: heavier than the codebase's explicit-SQL idiom.
- Hand-writing the guard predicate at each mutation site — rejected: copies of a load-bearing predicate drift.
