---
concept: lifecycle-handler
status: as-is
aliases:
  - reactive handler
references:
  - _discover/reactive-loops-and-lifecycle-handlers.md
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - _discover/error-policy-retry-loop-cap.md
---

# Lifecycle handler

## What it is

Per-node declarations in the template DSL that route executor (and acquisition) events into a small action vocabulary. Three lifecycle slots — `on_acquire_unavailable`, `on_executor_complete`, `on_executor_errored` — plus the `on_event:` map (per-event-name handler entries). Each slot has a `resolve` and an optional `invalidate`.

## Purpose

Declarative reactive policy. Templates express "what does this node want to happen when its executor errors / completes / can't acquire" in YAML rather than requiring executors to reinvent retry / cascade decisions themselves.

## Template fields

```
on_acquire_unavailable:
  resolve: pass | retry | error
  invalidate: [targets]
on_executor_complete:
  resolve: by_changed | always_propagate | never_propagate
  invalidate: [targets]
on_executor_errored:
  resolve: pass | retry | error
  invalidate: [targets]
on_event:
  <event_name>:
    resolve: pass | retry | error
    invalidate: [targets]
```

## Boundaries

Owns: the three slots and the `on_event` map; the resolve verdicts per slot; the unconditional `invalidate` slot. Does NOT own: error-types policy chain (see `error-policy`), cascade firing (see `cascade`, `last-outcome`), claim release (see `auto-terminal`; the claimant-guarded release discipline per `@blessed-invariant 4` governs every `rimsky_claim_handles` delete and node-run `claimed_by` null), the end-to-end stitching from terminal event to producer verb (see `terminal-resolution`). Adjacent: `cascade`, `last-outcome`, `error-policy`, `on-event-handler`, `invalidate`, `terminal-resolution`.

Per `spec:2026-05-12-nomenclature-resolution` Group E.2, the `on_executor_blocked` slot is retired alongside the wire-level `Blocked` event collapse into `Error{error_class}`. All error variants now route through `on_executor_errored`; the `error_types:` policy map discriminates by `error_class`. Templates that previously declared `on_executor_blocked` migrate to `on_executor_errored` with an explicit `error_types: { executor_blocked: ... }` entry.

## Invariants

- Three lifecycle-handler slots plus the `on_event` map.
- Valid resolve verdicts per slot are fixed (`pass | retry | error` for acquire/errored; `by_changed | always_propagate | never_propagate` for complete; `pass | retry | error` per `on_event` entry).
- `pass` / `error` resolutions on `on_acquire_unavailable` / `on_executor_errored` call `Abandon` on already-Open'd claims (matching `handleOrphanedClaim` semantics).
- Per-emit `frame: in | next` discipline applies (default `next`).
- `invalidate` slot fires unconditionally alongside `resolve`.
- The runtime-apply entry point is `code:runtime/runner_terminal_handlers.go::applyTerminalError` (post-`spec:2026-05-12-nomenclature-resolution` E.9 collapse); `applyTerminalBlockedOrErrored` is retired.

## Aliases and historical names

This concept owns three single-slot handlers (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_errored`). The sibling `on_event` map is structurally different (key-indexed `{event_name → handler}`, not a single slot) and is its own concept — see `on-event-handler`. Older prose that counted the surface as "5 slots" (e.g. `docs/concepts/handlers.md`) collapses the two shapes; pre-2026-05-12 it was "4 slots plus the `on_event` map" — the catalog now keeps them distinct at three slots.

## Open within this concept

(none live; previously open tensions on slot-count drift and `Blocked`-vs-`Errored` routing were resolved by `spec:2026-05-12-nomenclature-resolution` Groups E.2 / E.9 / E.10.)

## Notes

- Slot count 4→3 under `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #9, Group E.10). The `on_executor_blocked` slot retired alongside the wire-level `Blocked` event collapse into `Error{error_class}` (Group E.2). Runtime-apply entry consolidated to `applyTerminalError` (E.9). Resolves `tension:_resolved/blocked-vs-errored-routing` and `tension:_resolved/handler-slot-count-drift`.
- 2026-05-14: the three lifecycle slots (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_errored`) lose their `invalidate.targets:` clauses; `resolve` and `error_class` stay. The cross-reference to `concept:on-event-handler` is dropped (that concept is retired). The concept reduces to "three lifecycle slots with `resolve` + `error_class`." Cascade coupling is declared receiver-side via `concept:node-subscription`. Per spec `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.

