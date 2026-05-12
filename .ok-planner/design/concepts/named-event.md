---
concept: named-event
status: as-is
aliases: []
references:
  - _discover/named-events-and-on-event-handlers.md
  - _discover/2026-05-10-event-log-append-only-jsonb.md
  - _discover/2026-05-10-attribute-substitution-grammar.md
---

# Named event

## What it is

A `NamedEvent` is a non-terminal executor emission carrying a name (string from `Capabilities.declared_events`) and an opaque payload. Persisted to `rimsky_node_events` (with inline/handle spill via `BlobBackend`). Two consumption paths: attribute substitution (`{{nodes.<emitter>.event.<name>.<json_path>}}`) and the per-node `on_event:` handler map.

## Purpose

A graph node's executor often produces signal worth driving other nodes mid-run (progress events, per-step scores, partial outputs). Rolling them into the terminal vocabulary would couple them to dispatch lifecycle; a separate non-terminal channel keeps them clean.

## Boundaries

Owns: the emission protocol surface, the persistence ledger, the two consumption paths, the `declared_events` registration cross-check. Does NOT own: terminal events (those close the stream), audit log shape (see `event-log`). Adjacent: `executor`, `on-event-handler`, `event-log`, `attribute` (substitution consumer), `blob-backend` (spill).

## Invariants

- Event payloads are inert in rimsky (`@blessed-invariant 21`). Read only at the `walkPath` substitution leaf and the persistence-layer fetch.
- Most-recent emission of `(emitter, name)` wins at substitution time; full history retained in the ledger.
- `on_event` keys are cross-checked against the executor's `Capabilities.declared_events` at template registration when the executor is reachable; unknown event names at runtime are treated as no-ops.

## Ledger storage

The persisted form of named events is `rimsky_node_events`, an append-only ledger with columns `emitter_node_type`, `event_name`, `payload_inline` / `payload_handle` / `payload_handle_backend`, `seq`. Payloads can be spilled via the configured `BlobBackend` (per `persistence.blob.backend` ∈ {`inline` | `pg-largeobject` | `filesystem` | `memory`}). Read by attribute substitution `{{nodes.<emitter>.event.<name>.<path>}}` and by `on_event` handlers (see `on-event-handler`).

Opacity discipline (`@blessed-invariant 21`): the payload bytes are inert in rimsky — read only via `walkPath` substitution and the persistence-layer fetch on event consumption. Never logged, formatted with `%v`, validated beyond schema gates, transformed, attached to traces, or included in error messages.

Most-recent emission of `(emitter, event_name)` wins at substitution time. No built-in retention; operator-managed.

## Aliases and historical names

None live.

## Open within this concept

(no live tensions.)

