---
decision: advisory-locks
status: as-is
---

# Named cross-process coordination

## Choice

Postgres advisory locks + sqlite equivalent, scoped per job. Migration ownership and scheduler-tick ownership hold session-scoped locks for the duration of the migration batch or the tick. Per-scope serialization (named locks and claim-scope locks) holds transaction-scoped locks for the duration of the acquiring transaction.

## Rationale

Three distinct coordination jobs, each held exactly as long as it needs to be: session-scoped for the two whole-process jobs (migration, scheduler tick), transaction-scoped for the per-scope job that fires inside an acquisition transaction and must release when that transaction ends.
