---
concept: event-log
status: as-is
aliases:
  - audit log
  - rimsky_events table
references:
  - _discover/2026-05-10-event-log-append-only-jsonb.md
---

# Event log (audit log)

## What it is

`rimsky_events` — rimsky's internal append-only audit log. Schema: `id BIGSERIAL`, `instance_id`, `node_id`, `kind TEXT` (free-form, no enum CHECK), `payload JSONB`, `occurred_at TIMESTAMPTZ`. Indexed by `(node_id, occurred_at DESC)`, `(instance_id, ...)`, `(kind, ...)`. Written by rimsky's supervisor / scheduler / control-api at observable transitions. Read by the `/events` route in `cascade-graph` for the operator dashboard.

## Purpose

Rimsky needs an append-only record of "what happened" for incident review, operator dashboards, and debugging — a record rimsky owns (rimsky-readable JSONB, not bound by `@blessed-invariant 21` opacity). The free-form `kind` column lets new event categories appear with zero migration; the price is that typos produce events no consumer finds.

## Boundaries

Owns: the `rimsky_events` schema, the CRUD path, the read pattern feeding `cascade-graph`. Does NOT own: the named-event ledger (`rimsky_node_events` — see `named-event` "Ledger storage" subsection), retention policy (operator-managed), interpretation of individual `kind` strings (lives in consumers). Adjacent: `cascade-graph` (reads from `/events`), `observability`, `named-event` (sibling append-only table with different opacity discipline).

## Invariants

- `rimsky_events.kind` is free-form; no enum CHECK. Zero-migration to add a new kind; typos produce events no consumer finds.
- `rimsky_events.payload` is rimsky's own JSONB — readable by rimsky for the dashboard and audit consumers. NOT bound by `@blessed-invariant 21` (which governs the named-event ledger).
- No built-in retention; operator-managed retention is required.

## Aliases and historical names

Pre-`2026-05-11-design-log-convergence`, this concept also covered `rimsky_node_events` (named-event ledger). That material moved to `concepts/named-event.md` "Ledger storage" subsection. Filename `event-log.md` retained; content is now audit-log-only.

## Open within this concept

- `rimsky_events.kind` is free-form — see `tensions/events-kind-no-enum.md`.
