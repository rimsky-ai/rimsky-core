---
topic: advisory-locks-tick-and-migrate
kind: discipline
---

# Advisory locks: scheduler tick, migration session, per-name and per-scope acquisition

## Description

Rimsky exposes four advisory-lock primitives on the `persistence.AdvisoryLocker` interface (`foundation/persistence/driver.go:53-82`) — `TrySchedulerTick`, `AcquireMigrationLock`, `TakeNamedLockInTx`, `TakeScopeLockInTx`. Each maps to a Postgres `pg_advisory_*` call when running against the postgres driver and to in-process equivalents (`sync.Mutex`, no-op) on SQLite, where the writer-only-on-one-host topology subsumes the cross-process exclusion.

The two long-lived keys are pinned: `SCHEDULER_TICK_KEY = 4853127298010834892` (`foundation/persistence/postgres/advisory_locker.go:23-25`) and `advisoryMigrationLockKey = 5412893270184856212` (line 27-28). The file warns explicitly: "Never reuse these int64s elsewhere." Both keys are used at the session level (Postgres's `pg_try_advisory_lock` / `pg_advisory_lock`); the migration lock specifically pins a dedicated `*pgxpool.Conn` for the duration of the batch (`AcquireMigrationLock` at line 63-82) so the other pool connections remain available to migration statements.

The per-name and per-scope helpers (`TakeNamedLockInTx` line 92-100, `TakeScopeLockInTx` line 102-118) use `pg_advisory_xact_lock(hashtext('rimsky_lock:'+name))` and `pg_advisory_xact_lock(hashtext('rimsky_scope:'+store+':'+hex(scope)))` respectively. Both run inside the supplied `pgx.Tx` and release at COMMIT / ROLLBACK. They are no-ops on SQLite (where `BEGIN IMMEDIATE` serializes writers globally).

The reason these matter is documented at `foundation/integration/runner_acquire.go:538-557`: under READ COMMITTED, two supervisors can each pass the in-Go conflict predicate against the other's still-uncommitted INSERT into `rimsky_claim_handle`; the per-named-lock and per-scope advisory lock closes that window before the conflict check even runs. The acquisition flow walks `(lock_kind, sort_key)` order (the deterministic lock-ordering rule) so two concurrent acquirers that need both N1 and S1 always queue on the same advisory lock first, preventing the (N1-held, S1-wait) ⨯ (S1-held, N1-wait) deadlock.

CLAUDE.md's "Blessed invariants" list cites these four primitives at items 3, 4b, 7, 8, 10. The advisory locker is the only way to take per-name or per-scope exclusion: depguard's `pgx-isolation` rule (`.golangci.yml:14-30`) forbids reaching into `pgx` outside its allow-list, which means modeling code cannot bypass `AdvisoryLocker` to issue its own `pg_advisory_xact_lock`.

## Code surface

- `foundation/persistence/driver.go:53-82` — `AdvisoryLocker` interface declaration.
- `foundation/persistence/postgres/advisory_locker.go` (entire file; ~150 lines).
- `foundation/persistence/sqlite/advisory_locker.go` — SQLite no-op / mutex fallbacks.
- `foundation/persistence/migrations.go:20-50` — session-lock-around-batch wrapper.
- `foundation/integration/runner_acquire.go:538-557` — per-scope advisory-lock acquisition inside the runner.
- `foundation/integration/conductor.go:30-50` — tick-lock site.
- `.golangci.yml:14-30` — `pgx-isolation` enforcement.

## Prose surface

- `CLAUDE.md` "Blessed invariants" §3, §4b, §7, §8, §10.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — foundation-side responsibility for advisory locking.

## Adjacent topics

- `2026-05-10-named-and-scope-locks-deterministic-order` — uses the per-name and per-scope advisory primitives.
- `2026-05-10-atomic-acquisition-decoupled-tx` — wraps the advisory locks inside the acquisition tx.
- `2026-05-10-pre-v1-break-freely-migrations` — migration runner uses `AcquireMigrationLock`.
- `2026-05-10-postgres-only-runtime-state` — three runtime processes coordinate via these advisory locks.

## Observations

- The migration-lock key (`5412893270184856212`) and scheduler-tick key (`4853127298010834892`) are both magic int64s with no central registry beyond the comment in `advisory_locker.go:20`. A future helper that adds a third long-lived advisory lock has only this comment as the trip-wire.
- SQLite's `TakeNamedLockInTx` / `TakeScopeLockInTx` are documented as "no-op" (file `foundation/persistence/sqlite/advisory_locker.go`) on the grounds that BEGIN IMMEDIATE serializes the writer globally. This is sound for single-process dev but breaks the moment a future operator runs SQLite + replicas > 1 — the same "memory blob backend rejected outside unified" gate at `foundation/persistence/blob_config.go:115` would apply by analogy but is not enforced.
- The SQLite migration lock is called `sync.Mutex` in code but the migration runner uses a separate "acquire a dedicated conn" idiom on Postgres; the SQLite path acquires the in-process mutex around the same batch boundary, so the semantics match without a connection pin.
- `pg_advisory_xact_lock` releases at COMMIT (not at the end of the SELECT statement); a long-running acquisition tx holds the per-scope lock for its full duration. This is intentional but worth noting.
