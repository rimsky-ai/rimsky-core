---
concept: persistence-database
status: as-is
aliases: [persistence-driver]
---

# Persistence database

## What it is

The top-level database interface is the umbrella over the rimsky persistence layer. One database is constructed per process (a single open call); the three runtime processes hold it for their lifetime and close it on shutdown. Analogous to a stdlib-style database handle — the runtime object, not the adapter. It exposes the container methods that hand back the queue, the per-row-type table accessors, the advisory locker, the migration runner, a ping/healthcheck, a blob-backend setter, and close.

The per-row-type accessor umbrella is the bundle returned by the database interface. It aggregates the per-row-type accessors (templates, nodes, frames, instances, claim-handles, claim-holders, etc.). Most callers depend on only a subset; the umbrella keeps startup wiring compact.

Each row kind has its own singular per-row accessor sub-interface — one per persisted ledger (templates and their tags, instances, lifecycle-idempotency rows, nodes, claim-handles, node attributes, claim-holders, events, schedules, supervisor rows, frames, blob-orphans, node-events). The accessor-bag methods that hand these back stay plural; the singular-accessor-vs-plural-bag split mirrors Go-stdlib convention for one-row-of-many APIs.

Two impls: a Postgres adapter (the default outside the all-in-one deployment) and an SQLite adapter (the all-in-one default; safe for processes sharing one local database file). Alongside the per-row accessors, the umbrella exposes an advisory locker and a queue facility; a single shared migration runner keeps migrations from forking across adapters.

The adapter selector — a string-valued driver config field ("postgres" / "sqlite") — is distinct from the database interface. "Driver" names the adapter shape.

Row-struct convention: row structs stay singular even though the persisted tables are plural — the node, frame, claim-handle, and node-run row structs map to the corresponding pluralized node, frame, claim-handle, and node-run ledgers.

## Purpose

Single abstraction so graph and control code (and the supervisor's integration runner) never touch the raw Postgres driver directly — an enforced import boundary keeps the driver isolated behind the database interface. Lets SQLite back testing-fast scenarios and lets a future third driver plug in.

## Boundaries

Owns: the top-level database container interface, the per-row-type accessor umbrella, the per-row-type accessor sub-interfaces, the two impls, the migration runner. Does NOT own: schema content (that lives in the migration files), connection-pool sizing (operator config). Adjacent: `advisory-lock`, `blob-backend`, `node-run`, every persistence-typed concept.

## Invariants

- The SQLite driver is safe for multiple rimsky processes sharing one local database file: its read-then-write operations are transactional (immediate-mode transactions hold the writer slot), so cross-process atomicity holds. Separate database files per process and network filesystems are unsupported and undetectable from inside a process. There is no startup gate — the platform defaults to Postgres outside the all-in-one deployment, and an operator overriding to SQLite is presumed to have chosen deliberately.
- The memory blob backend IS gate-rejected outside the unified single-process role.
- The raw-Postgres-driver isolation rule restricts direct driver use to the Postgres adapter, its test helpers, the binary entrypoints, the scenario harness, the bundled services, and the smoke-test harness — graph and control code go through the database interface.
- Pre-v1 migration discipline: filenames are append-only; SQL inside is free to drop+recreate.
