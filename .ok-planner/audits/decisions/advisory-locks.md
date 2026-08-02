---
audit: advisory-locks
artifact: decision:advisory-locks
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:43:58Z
---

# Session-scoped vs transaction-scoped advisory locking, postgres and sqlite

Supported. Checked both backends' `AdvisoryLocker` implementations (5 methods each, the interface's full population): the postgres driver holds `TrySchedulerTick`/`AcquireMigrationLock` on one pooled connection released explicitly by the caller (session-scoped, for the tick and the migration batch) and `TakeNamedLock`/`TakeClaimScopeLock`/`TakeLifecycleScopeLock` via `pg_advisory_xact_lock` inside the caller's transaction (transaction-scoped, releasing at commit/rollback). The sqlite driver holds the tick and migration locks via cross-process file locks keyed off the database path (the session-scoped equivalent) and implements the three per-scope lock calls as no-ops; a dedicated test (`TestTakeNamedLock_MutualExclusionComesFromImmediateTxLock`) demonstrates the no-op is sound because the `_txlock=immediate` DSN pragma already makes every transaction exclusive at BEGIN, subsuming the same serialization window the per-scope locks close in postgres — this reasoning is also recorded in `concept:advisory-lock`'s invariants.
