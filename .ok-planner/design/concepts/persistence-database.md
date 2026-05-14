---
concept: persistence-database
status: as-is
aliases: [persistence-driver]
references:
  - _discover/2026-05-10-postgres-only-runtime-state.md
  - _discover/2026-05-10-sqlite-dev-only.md
  - _discover/2026-05-10-depguard-enforced-package-boundaries.md
  - _discover/2026-05-10-pre-v1-break-freely-migrations.md
---

# Persistence database

## What it is

`code:foundation/persistence/database.go::Database` is the top-level umbrella over the rimsky persistence layer. One `Database` is constructed per process (`Open()`); the three runtime processes hold it for their lifetime and `Close()` it on shutdown. Analogous to Go stdlib `sql.DB` — the runtime object, not the adapter. It exposes the container methods `Queue()`, `Tables()`, `AdvisoryLocker()`, `Migrate()`, `Ping()`, `SetBlobBackend()`, `Close()`.

`code:foundation/persistence/tables.go::Tables` is the per-row-type accessor umbrella returned by `Database.Tables()`. It aggregates the per-row-type accessors (`Templates()`, `Nodes()`, `Frames()`, `Instances()`, `ClaimHandles()`, `ClaimHolders()`, etc.). Most callers depend on only a subset; the umbrella keeps startup wiring compact.

Per-row-type sub-interfaces follow the singular `<RowKind>Table` convention: `TemplateTable`, `TemplateTagTable`, `InstanceTable`, `LifecycleIdempotencyTable`, `NodeTable`, `ClaimHandleTable`, `NodeAttributeTable`, `ClaimHolderTable`, `EventTable`, `ScheduleTable`, `SupervisorTable`, `FrameTable`, `BlobOrphanTable`, `NodeEventTable`. The bag-method names on `Tables` stay plural (`Templates()`, `Nodes()`, etc.) — the singular vs plural split mirrors Go-stdlib convention for one-row-of-many APIs.

Two impls: `foundation/persistence/postgres/` (production) and `foundation/persistence/sqlite/` (dev). Sub-interfaces: `AdvisoryLocker`, `Queue`, plus the per-row-type `<RowKind>Table` accessors hung off `Tables()`. Shared `Migrator` (`migrations.go`) so migrations don't fork.

The adapter selector — `code:foundation/persistence/types.go::Config.Driver` (string "postgres" / "sqlite") — is distinct from the `Database` interface and stays as-is. "Driver" is correctly used there to name the adapter shape.

Row-struct convention: Go-side row structs stay singular even though the SQL tables are plural — `NodeRow`, `FrameRow`, `ClaimHandleRow`, `NodeRunRow` (table: `rimsky_nodes`, `rimsky_frames`, `rimsky_claim_handles`, `rimsky_node_runs` post-`spec:2026-05-12-nomenclature-resolution` baseline rebase).

## Purpose

Single abstraction so graph and control code (and the supervisor's integration runner) never touch pgx directly (depguard `pgx-isolation` enforces). Lets sqlite back testing-fast scenarios and lets a future third driver plug in.

## Boundaries

Owns: the `Database` container interface, the `Tables` per-row-type accessor umbrella, the per-row-type `<RowKind>Table` interfaces, the two impls, the migration runner. Does NOT own: schema content (that lives in `migrations/*.sql`), connection-pool sizing (operator config). Adjacent: `advisory-lock`, `blob-backend`, `node-run`, every persistence-typed concept.

## Invariants

- SQLite is dev-only — multi-host requires Postgres. Documented but NOT gate-rejected.
- Memory blob backend IS gate-rejected outside `RIMSKY_PROCESS_ROLE=unified`.
- depguard `pgx-isolation` allow-list: only `foundation/persistence/postgres/`, `foundation/internal/pgtest/`, `cmd/`, `internal/pgtest/`, `graph/scenario/`, `stores/`, `test/smoke/`.
- Pre-v1 migration discipline: filenames are append-only; SQL inside is free to drop+recreate.

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution` baseline rebase, the migration history threaded through ~20 numbered files capturing the `rimsky_dispatch` → `rimsky_worker_request` → `rimsky_node_runs` renames, the `consumer_key` → `instance_key` rename, the `rimsky_lock_holders` → `rimsky_claim_handles` rename + plural shift, the `rimsky_frames.mode` → `frame_resolution_mode` rename, and `rimsky_lifecycle_idempotency` → `rimsky_lifecycle_idempotencies` plural shift. Post-rebase the chain collapses to a single `001-baseline.sql` reflecting the final schema; dev Postgres requires `DROP SCHEMA public CASCADE; CREATE SCHEMA public;` before re-applying (per `spec:2026-05-12-nomenclature-resolution` Group A).

## Open within this concept

- SQLite-multi-replica silent-split is documented but not enforced; memory backend IS — asymmetric gating, see `tensions/sqlite-vs-memory-reject-asymmetry.md`.

## Notes

- Renamed from `concept:persistence-driver` per the deferred B.7 follow-up of `spec:2026-05-12-nomenclature-resolution`. Top-tier interface renamed Driver→Database to match its actual role (runtime object, not adapter — analogous to Go stdlib sql.DB). Per-row-type sub-interfaces normalized to singular `<RowKind>Table` form. The `Store` accessor method on the top tier renamed to `Tables`. `cfg.Driver` (the string config field selecting "postgres" vs "sqlite") stays — that's the adapter selector and correctly named.
