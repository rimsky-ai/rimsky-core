---
concept: advisory-lock
status: as-is
aliases: []
---

# Advisory lock

## What it is

Five advisory-lock primitives on the persistence-layer advisory-locker interface: scheduler-tick, migration, per-name (in-tx), per-claim-scope (in-tx), and per-lifecycle-scope (in-tx). Postgres uses native session/transaction advisory locks. Under SQLite the scheduler-tick and migration locks are file-lock-based (lock files derived from the database path), holding exclusion across processes that share the database file on one host; the per-name, per-claim-scope, and per-lifecycle-scope in-tx locks are no-ops because the immediate-mode transaction's writer-slot hold subsumes them and is itself cross-process.

## Purpose

Cross-process coordination — through Postgres advisory locks, or under SQLite through file locks alongside the database file. The scheduler-tick lock makes the tick safely multi-replica; the migration lock serializes migrate runs; the per-name and per-claim-scope advisory locks close the READ COMMITTED window in the acquisition tx; the per-lifecycle-scope lock serializes the lifecycle fan-out's [check idempotency row, deliver, mark row] section across control-api/scheduler/supervisor replicas so racing fan-outs for one scope converge to a single delivery.

## Boundaries

Owns: the five primitives, the two pinned long-lived keys (scheduler-tick and migration), the session-vs-transaction scope difference. Does NOT own: the conflict matrix that decides which lock modes coexist, `max_quiet_period` cutoffs, the claim-handle ledger. Adjacent: `claim-handle`, `message` (scheduler-tick lock), `persistence-database` (migration lock), `supervisor` (the acquisition tx).

## Invariants

- Scheduler tick uses a non-blocking try-acquire on the pinned tick key (Postgres) or a non-blocking exclusive file lock (SQLite) — in both backends the exclusion holds across OS processes (invariant 7).
- For the scheduler-tick lock, an error from the lock attempt is treated as lock-held: the sweep pass is skipped, never run unlocked. The sweeps are periodic recovery, so a one-interval delay is benign, while running unlocked permits the concurrent sweeping the lock exists to prevent.
- Migration uses a blocking exclusion held for the duration of the batch — a session-level advisory lock (Postgres) or an exclusive file lock (SQLite), cross-process in both backends (invariant 8).
- Per-name, per-claim-scope, and per-lifecycle-scope advisory locks are transaction-scoped, released at COMMIT/ROLLBACK (Postgres); under SQLite they are no-ops, since the immediate-mode transaction's writer-slot hold already closes the same window. This makes the SQLite no-op sound only when the whole guarded section runs inside one transaction — the lifecycle fan-out therefore wraps [check row, deliver, mark row] in a single tx.
- All multi-lock acquisitions walk a deterministic order keyed by lock kind then sort key (invariant 3).
- The scheduler-tick and migration keys are two distinct pinned int64 values; a newly introduced pinned key must never collide with either.
