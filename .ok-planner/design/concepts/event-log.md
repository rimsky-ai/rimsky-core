---
concept: event-log
status: as-is
aliases:
  - audit log
references:
  - _discover/2026-05-10-event-log-append-only-jsonb.md
  - _discover/named-events-and-on-event-handlers.md
---

# Event log

## What it is

Two append-only tables share the word "events" but serve different purposes:

- **`rimsky_events`** — the rimsky-internal audit log. Columns `id BIGSERIAL`, `instance_id`, `node_id`, `kind TEXT` (free-form, no enum CHECK), `payload JSONB`, `occurred_at`. Indexed by `(node_id, occurred_at DESC)`, `(instance_id, ...)`, `(kind, ...)`. Written by rimsky's supervisor/scheduler at observable transitions. Drives `/events` dashboard paging.
- **`rimsky_node_events`** — the executor-emitted `NamedEvent` ledger (see `named-event`). Different shape: `emitter_node_type`, `event_name`, `payload_inline`/`payload_handle`/`payload_handle_backend`, `seq`. Spillable via `BlobBackend`. Read by attribute substitution `{{nodes.<emitter>.event.<name>.<path>}}` and `on_event` handlers.

## Purpose

Two different consumers want two different read patterns and opacity disciplines. `rimsky_events.payload` is rimsky's own JSONB (rimsky can read it for the dashboard). `rimsky_node_events.payload_*` is opaque per `@blessed-invariant 21`.

## Boundaries

Owns: both tables' schemas, their CRUD packages, their read patterns. Does NOT own: how individual `kind` strings or `event_name` strings get interpreted (those live in consumers). Adjacent: `named-event`, `observability`, `blob-backend`, and the cascade-graph endpoint (a sub-endpoint of `observability` that reads from these tables for the operator dashboard).

## Invariants

- `rimsky_events.kind` is free-form; no enum CHECK. Zero-migration to add a new kind; typos produce events no consumer finds.
- `rimsky_node_events.payload_*` is inert in rimsky (`@blessed-invariant 21`).
- Most-recent emission of `(emitter, name)` wins at substitution time.
- Neither table has built-in retention; operator-managed retention is required.

## Aliases and historical names

None live; the two tables are post-Phase-5 / post-platform-extensions.

## Open within this concept

- The two tables share the noun "events" but are independent — see `tensions/events-table-name-overlap.md`.
- `rimsky_events.kind` is free-form — see `tensions/events-kind-no-enum.md`.

