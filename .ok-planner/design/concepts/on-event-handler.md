---
concept: on-event-handler
status: as-is
aliases: []
references:
  - _discover/reactive-loops-and-lifecycle-handlers.md
  - _discover/named-events-and-on-event-handlers.md
---

# On-event handler

## What it is

`on_event` is the fifth declarable handler surface on a node: a key-indexed map `{event_name → handler}` that dispatches per executor-emitted named event. Structurally distinct from the four single-slot lifecycle handlers (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`), which each declare a single resolver. Shares the resolve+invalidate vocabulary (`resolve: pass | error`; `invalidate: {targets, frame}`) with the four lifecycle handlers.

## Purpose

Executors emit named events mid-run (`emit` is a non-terminal event with name + opaque payload). `on_event` lets templates react per event name — invalidating downstream nodes, transitioning the emitting node, or marking-as-passed — without coupling reactive policy to terminal-event handling.

## Boundaries

Owns: the map declaration in the template, the per-event-name resolver lookup at executor-emission time, the capabilities cross-check at template registration. Does NOT own: the named-event ledger storage (see `named-event`), the four single-slot lifecycle handlers (see `lifecycle-handler`), the discovery cache that powers the registration-time check (see `discovery-cache`). Adjacent: `lifecycle-handler`, `named-event`, `node`, `discovery-cache`.

## Invariants

- `on_event` is validated against `Capabilities.declared_events` at template registration when the peer is reachable via the observability handshake. The discovery cache supplies the declared-events list.
- Runtime treats unknown event names as no-ops if the peer was unreachable at registration (silent-skip; no error).
- Per-event-name handlers share the same resolve/invalidate vocabulary as the four lifecycle handlers; the per-emit `frame: in | next` discipline applies identically.

## Aliases and historical names

None live. The map shape was added under the reactive-loops design (`.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`).

## Open within this concept

(no live tensions distinct from `lifecycle-handler` and `named-event`.)
