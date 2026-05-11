---
concept: advisory-lock
status: as-is
aliases: []
references:
  - _discover/2026-05-10-advisory-locks-tick-and-migrate.md
  - _discover/2026-05-10-named-and-scope-locks-deterministic-order.md
  - _discover/2026-05-10-atomic-acquisition-decoupled-tx.md
---

# Advisory lock

## What it is

Four advisory-lock primitives on `persistence.AdvisoryLocker` (`foundation/persistence/driver.go:53-82`): `TrySchedulerTick`, `AcquireMigrationLock`, `TakeNamedLockInTx`, `TakeScopeLockInTx`. Postgres uses `pg_advisory_*`; SQLite degrades to `sync.Mutex` / no-op.

## Purpose

Cross-process coordination through Postgres (or `sync.Mutex` in single-process dev). The scheduler-tick lock makes the tick safely multi-replica; the migration lock serializes migrate runs; the per-name and per-scope advisory locks close the READ COMMITTED window in the acquisition tx.

## Boundaries

Owns: the four primitives, the two pinned long-lived keys (`SCHEDULER_TICK_KEY`, `advisoryMigrationLockKey`), the session-vs-transaction scope difference. Does NOT own: the conflict matrix (`ModeCoexists`), heartbeat cutoffs, the claim-handle ledger. Adjacent: `scheduler`, `migrate`, `claim-handle`, `supervisor` (the acquisition tx).

## Invariants

- Scheduler tick uses `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` (Postgres) or `sync.Mutex` (SQLite) (`@blessed-invariant 7`).
- Migration uses session-level `pg_advisory_lock` for the duration of the batch (`@blessed-invariant 8`).
- Per-name and per-scope advisory locks are transaction-scoped (`pg_advisory_xact_lock`), released at COMMIT/ROLLBACK.
- All multi-lock acquisitions walk `(lock_kind, sort_key)` deterministic order (`@blessed-invariant 3`).
- Two pinned int64 keys are documented as "never reuse" in code (`advisory_locker.go:20-28`).

## Aliases and historical names

None live.

## Open within this concept

- SQLite advisory-lock no-op semantics under multi-host break the cross-process exclusion silently — adjacent to `tensions/sqlite-vs-memory-reject-asymmetry.md`.

