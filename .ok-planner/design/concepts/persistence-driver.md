---
concept: persistence-driver
status: as-is
aliases: []
references:
  - _discover/2026-05-10-postgres-only-runtime-state.md
  - _discover/2026-05-10-sqlite-dev-only.md
  - _discover/2026-05-10-depguard-enforced-package-boundaries.md
  - _discover/2026-05-10-pre-v1-break-freely-migrations.md
---

# Persistence driver

## What it is

`persistence.Driver` (`foundation/persistence/driver.go`) is the umbrella interface families that abstract Postgres-vs-SQLite. Two impls: `foundation/persistence/postgres/` (production) and `foundation/persistence/sqlite/` (dev). Sub-interfaces: `AdvisoryLocker`, `Queue`, `Store`, `LockHoldersStore`, `ClaimHoldersStore`, `FrameStore`, etc. Shared `Migrator` (`migrations.go`) so migrations don't fork.

## Purpose

Single abstraction so modeling and integration code never touch pgx directly (depguard `pgx-isolation` enforces). Lets sqlite back testing-fast scenarios and lets a future third driver plug in.

## Boundaries

Owns: the interface families, the two impls, the migration runner. Does NOT own: schema content (that lives in `migrations/*.sql`), connection-pool sizing (operator config). Adjacent: `advisory-lock`, `blob-backend`, `worker-request`, every persistence-typed concept.

## Invariants

- SQLite is dev-only — multi-host requires Postgres. Documented but NOT gate-rejected.
- Memory blob backend IS gate-rejected outside `RIMSKY_PROCESS_ROLE=unified`.
- depguard `pgx-isolation` allow-list: only `foundation/persistence/postgres/`, `foundation/internal/pgtest/`, `cmd/`, `modeling/internal/pgtest/`, `modeling/scenario/`, `stores/`, `test/smoke/`.
- Pre-v1 migration discipline: filenames are append-only; SQL inside is free to drop+recreate.

## Aliases and historical names

None live.

## Open within this concept

- SQLite-multi-replica silent-split is documented but not enforced; memory backend IS — asymmetric gating, see `tensions/sqlite-vs-memory-reject-asymmetry.md`.

