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

Per-node declarations in the template DSL that route executor (and acquisition) events into a small action vocabulary. Four lifecycle slots — `on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored` — plus the `on_event:` map (per-event-name handler entries). Each slot has a `resolve` and an optional `invalidate`.

## Purpose

Declarative reactive policy. Templates express "what does this node want to happen when its executor blocks / errors / completes / can't acquire" in YAML rather than requiring executors to reinvent retry / cascade decisions themselves.

## Boundaries

Owns: the four slots and the `on_event` map; the resolve verdicts per slot; the unconditional `invalidate` slot. Does NOT own: error-types policy chain (see `error-policy`), cascade firing (see `cascade`, `last-outcome`), claim release (see `auto-terminal`, `claimant-guarded`), the end-to-end stitching from terminal event to producer verb (see `terminal-resolution`). Adjacent: `cascade`, `last-outcome`, `error-policy`, `on-event-handler`, `invalidate`, `terminal-resolution`.

## Invariants

- Valid resolve verdicts per slot are fixed (`pass | retry | error` for acquire/blocked/errored; `by_changed | always_propagate | never_propagate` for complete; `pass | retry | error` per `on_event` entry).
- `pass` / `error` resolutions on `on_acquire_unavailable` / `on_executor_blocked` / `on_executor_errored` call `Abandon` on already-Open'd claims (matching `handleOrphanedClaim` semantics).
- Per-emit `frame: in | next` discipline applies (default `next`).
- `invalidate` slot fires unconditionally alongside `resolve`.

## Aliases and historical names

The slot list is sometimes counted as "4 handlers + on_event map" (CLAUDE.md "Vocabulary") and sometimes as "5 slots" (`docs/concepts/handlers.md`). Both framings are correct; the `on_event` map is shaped differently (key-indexed) but uses the same resolve+invalidate vocabulary.

## Open within this concept

- Handler-slot-count framing drift (4 vs 5) — see `tensions/handler-slot-count-drift.md`.
- Error-action count drift (3 vs 5) across CLAUDE.md and `docs/concepts/error-policy.md` — see `tensions/error-action-count-drift.md`.

