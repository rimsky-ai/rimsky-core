---
concept: node-state
status: retired
aliases:
  - state (column)
references:
  - _discover/2026-05-10-state-machine-no-self-loop.md
  - _discover/2026-05-10-parked-state-and-resume.md
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - ../../../specs/2026-05-15-data-platform-extensions-design.md
---

> **Retired** by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. State now lives entirely on `rimsky_node_runs`; the `rimsky_nodes.state` column is removed. The five-state machine (`fresh | stale | running | failed | parked`) and its transition table survive — they apply per-run, not per-node — and are documented under `concept:node-run`. The retirement is part of the run-tree extension that promoted runs to a tree with state aggregation, sub-graph composition, and durable claims.

# Node state

## What it is

`node-state` is the small enum that describes "where is this node right now": `fresh | stale | running | failed | parked`. Stored as `rimsky_nodes.state` plus the explicit transition table in `foundation/cascade/state.go::NextState`. Distinct from `last-outcome` (which is per-resolution metadata, not a dispatch gate).

## Purpose

A small, exhaustively-enumerated state vocabulary keeps "is this node eligible to dispatch?" a one-column predicate. Packing per-resolution outcome into the state would balloon the vocabulary to 10+ values and turn the dispatch eligibility check into a multi-state predicate.

## Boundaries

`node-state` owns: legal transitions, the rejection of illegal ones (no idempotent shortcuts), the runtime predicate for dispatch eligibility. It does NOT own: cascade-firing decisions (those live on `last-outcome`), audit metadata (those live in `transition-reason`), claim disposition (those live on the claim handle row). Adjacent: `last-outcome`, `transition-reason`, `frame`, `parked-state`, `cascade`.

## Invariants

- `NextState(current, reason)` rejects `current == requested` under `dispatch_claimed` — no silent idempotent short-circuit (`@blessed-invariant 1`, `foundation/cascade/state.go:103-108`).
- `ReasonHandlerError` is a deliberate dead-end sentinel; legal in audit but rejected as a transition reason.
- `parked → fresh` is rejected; wake transitions go `parked → stale` then re-dispatch.

## Aliases and historical names

Pre-`parked` (pre-migration-006) prose listed four states. `running → running` once admitted an ergonomic idempotency check; that path was removed under the v3 redesign.

## Open within this concept

- The "4 vs 5 states" doc drift across CLAUDE.md and `docs/concepts/node-state.md` (see `tensions/state-count-drift.md`).
- `transition-reason` carries a richer audit vocabulary that overlaps `last-outcome` semantics (see `tensions/transition-reason-vs-last-outcome.md`).

