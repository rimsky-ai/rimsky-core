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

The top-level database interface is the umbrella over the rimsky persistence layer. One database is constructed per process (a single open call); the three runtime processes hold it for their lifetime and close it on shutdown. Analogous to Go stdlib `sql.DB` — the runtime object, not the adapter. It exposes the container methods that hand back the queue, the per-row-type table accessors, the advisory locker, the migration runner, a ping/healthcheck, a blob-backend setter, and close.

The per-row-type accessor umbrella is the bundle returned by the database interface. It aggregates the per-row-type accessors (templates, nodes, frames, instances, claim-handles, claim-holders, etc.). Most callers depend on only a subset; the umbrella keeps startup wiring compact.

Each row kind has its own singular per-row accessor sub-interface — one per persisted ledger (templates and their tags, instances, lifecycle-idempotency rows, nodes, claim-handles, node attributes, claim-holders, events, schedules, supervisor rows, frames, blob-orphans, node-events). The accessor-bag methods that hand these back stay plural; the singular-accessor-vs-plural-bag split mirrors Go-stdlib convention for one-row-of-many APIs.

Two impls: a Postgres adapter (production) and an SQLite adapter (dev). Alongside the per-row accessors, the umbrella exposes an advisory locker and a queue facility; a single shared migration runner keeps migrations from forking across adapters.

The adapter selector — a string-valued driver config field ("postgres" / "sqlite") — is distinct from the database interface and stays as-is. "Driver" is correctly used there to name the adapter shape.

Row-struct convention: row structs stay singular even though the persisted tables are plural — the node, frame, claim-handle, and node-run row structs map to the corresponding pluralized node, frame, claim-handle, and node-run ledgers, named in their final post-baseline-rebase form per `spec:2026-05-12-nomenclature-resolution`.

## Purpose

Single abstraction so graph and control code (and the supervisor's integration runner) never touch the raw Postgres driver directly — an enforced import boundary keeps the driver isolated behind the database interface. Lets SQLite back testing-fast scenarios and lets a future third driver plug in.

## Boundaries

Owns: the top-level database container interface, the per-row-type accessor umbrella, the per-row-type accessor sub-interfaces, the two impls, the migration runner. Does NOT own: schema content (that lives in the migration files), connection-pool sizing (operator config). Adjacent: `advisory-lock`, `blob-backend`, `node-run`, every persistence-typed concept.

## Invariants

- SQLite is dev-only — multi-host requires Postgres. Documented but NOT gate-rejected.
- The memory blob backend IS gate-rejected outside the unified single-process role.
- The raw-Postgres-driver isolation rule restricts direct driver use to the Postgres adapter, its test helpers, the binary entrypoints, the scenario harness, the bundled services, and the smoke-test harness — graph and control code go through the database interface.
- Pre-v1 migration discipline: filenames are append-only; SQL inside is free to drop+recreate.

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution` baseline rebase, the migration history threaded through ~20 numbered files capturing a chain of renames: the dispatch → worker-request → node-run ledger renames, the consumer-key → instance-key field rename, the lock-holder → claim-handle ledger rename plus pluralization, the frame `mode` field renamed to `frame_resolution_mode`, and the lifecycle-idempotency ledger pluralized. Post-rebase the chain collapses to a single baseline migration reflecting the final schema; dev Postgres requires dropping and recreating the public schema before re-applying (per `spec:2026-05-12-nomenclature-resolution` Group A).

## Open within this concept

- SQLite-multi-replica silent-split is documented but not enforced; the memory backend IS — asymmetric gating, see `tension:sqlite-vs-memory-reject-asymmetry`.

## Notes

- Renamed from `persistence-driver` per the deferred B.7 follow-up of `spec:2026-05-12-nomenclature-resolution`. The top-tier interface was renamed from "driver" to "database" to match its actual role (runtime object, not adapter — analogous to Go stdlib sql.DB). Per-row-type sub-interfaces normalized to the singular per-row-accessor form. The top-tier accessor method that hands back the per-row-type umbrella was renamed from "store" to "tables". The string config field selecting "postgres" vs "sqlite" stays named "driver" — that's the adapter selector and correctly named.
- 2026-05-24 — Migration history flattened per `spec:2026-05-24-instance-debugger-design`. The fourteen numbered migrations are deleted and replaced with a single consolidated baseline migration per backend reflecting current schema state plus the new breakpoint tables and a paused flag on the instance row. Pre-v1 break-freely operation; existing dev databases drop and recreate. Adds breakpoint and breakpoint-hit table accessors on the per-row-type accessor umbrella.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
