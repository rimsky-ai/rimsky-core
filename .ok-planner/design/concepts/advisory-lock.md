---
concept: advisory-lock
status: as-is
aliases: []
---

# Advisory lock

## What it is

Four advisory-lock primitives on the persistence-layer advisory-locker interface: scheduler-tick, migration, per-name (in-tx), and per-scope (in-tx). Postgres uses native session/transaction advisory locks; SQLite degrades to an in-process mutex / no-op.

## Purpose

Cross-process coordination through Postgres (or an in-process mutex in single-process dev). The scheduler-tick lock makes the tick safely multi-replica; the migration lock serializes migrate runs; the per-name and per-scope advisory locks close the READ COMMITTED window in the acquisition tx.

## Boundaries

Owns: the four primitives, the two pinned long-lived keys (scheduler-tick and migration), the session-vs-transaction scope difference. Does NOT own: the conflict matrix that decides which lock modes coexist, heartbeat cutoffs, the claim-handle ledger. Adjacent: `sensor` (scheduler-tick lock), `persistence-database` (migration lock), `claim-handle`, `supervisor` (the acquisition tx).

## Invariants

- Scheduler tick uses a non-blocking try-acquire on the pinned tick key (Postgres) or an in-process mutex (SQLite) (`@blessed-invariant 7`).
- Migration uses a session-level advisory lock held for the duration of the batch (`@blessed-invariant 8`).
- Per-name and per-scope advisory locks are transaction-scoped, released at COMMIT/ROLLBACK.
- All multi-lock acquisitions walk a deterministic order keyed by lock kind then sort key (`@blessed-invariant 3`).
- Two pinned int64 keys are documented as "never reuse" at the definition site.
