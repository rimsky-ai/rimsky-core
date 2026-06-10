---
concept: named-event
status: as-is
aliases: []
---

# Named event

## What it is

A named event is a non-terminal executor emission tagged with a name (a string drawn from the executor's `declared_events` capability) and an inert payload recorded alongside it. Persisted to a named-event ledger (with inline/handle spill via the blob backend, see `concept:blob-backend`). Two consumption paths: attribute substitution (`{{nodes.<emitter>.event.<name>.<json_path>}}`) and subscription to the event (see `concept:node-subscription`). Inertness discipline cross-linked at `concept:inertness`.

A named event is **consumed invalidate-then-pull**, not delivered. Subscribing to an event does not push the payload anywhere: an emission marks the subscribing receiver stale, the receiver is rescheduled, and on its next run it **pulls the latest** persisted emission via substitution. Consequently subscribing fires the receiver **once per frame regardless of how many times the event was emitted** (the wait-set collapses N emissions to one dispatch); the receiver always reads the most-recent emission. Named events **never create a frame** and do **not** fan out per-emission.

**Named events are not a fan-out mechanism.** True per-item (parallel) fan-out — one work unit per partition, processed concurrently — is `concept:fan-out` (claim-producer split-scope). Sequential per-message processing — one message per frame, processed in order — is `serial_queue` message delivery (see `concept:message`). A named event is neither: it is a reactive-recompute trigger that the receiver pulls from, not a per-emission dispatch source.

## Purpose

A graph node's executor often produces signal worth driving other nodes mid-run (progress events, per-step scores, partial outputs). Rolling them into the terminal vocabulary would couple them to dispatch lifecycle; a separate non-terminal channel keeps them clean.

## Boundaries

Owns: the emission protocol surface, the persistence ledger, the two consumption paths, the `declared_events` registration cross-check. Does NOT own: terminal events (those close the stream), audit log shape (see `event-log`). Adjacent: `executor`, `node-subscription`, `event-log`, `attribute` (substitution consumer), `blob-backend` (spill).

## Invariants

- Event payloads are inert in rimsky (`@blessed-invariant 21`). Read only at the sanctioned substitution leaf and the persistence-layer fetch.
- Most-recent emission of `(emitter, name)` wins at substitution time; full history retained in the ledger.
- Event subscription names are cross-checked against the executor's `declared_events` capability at template registration when the executor is reachable; unknown event names at runtime are treated as no-ops.

## Ledger storage

The persisted form of named events is an append-only ledger keyed by emitter node-type + event name + sequence, with each record carrying either the inline payload or a spill handle plus the backend that holds it. Payloads can be spilled via the configured blob backend (one of inline, Postgres large-object, filesystem, or in-memory — see `concept:blob-backend`). Read by attribute substitution `{{nodes.<emitter>.event.<name>.<path>}}` and by event subscriptions (see `concept:node-subscription`).

Inertness discipline (`@blessed-invariant 21`, see `concept:inertness`): the payload bytes are inert in rimsky — read only via the sanctioned substitution leaf and the persistence-layer fetch on event consumption. Never logged, formatted as a value, validated beyond schema gates, transformed, attached to traces, or included in error messages.

Most-recent emission of `(emitter, event_name)` wins at substitution time. No built-in retention; operator-managed.
