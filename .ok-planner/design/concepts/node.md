---
concept: node
status: as-is
aliases:
  - graph-node
references:
  - _discover/2026-05-10-state-machine-no-self-loop.md
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - _discover/2026-05-10-parked-state-and-resume.md
---

# Node

## What it is

A node is one declarative unit of work in a template's graph. Each node has a name, a type (template-author chosen string used as the dispatch routing key), zero-or-more dependencies on other nodes, zero-or-more required claims/locks, optional attributes JSON schema, optional userdata, optional lifecycle-handler block, optional `on_event` map, optional `quality_rules`, and (for non-claim-only nodes) a target executor. At runtime, a node materializes as a row in `rimsky_nodes` (keyed by `(instance_id, node_type)`) carrying `state`, `last_outcome`, `frame_id`, and per-node bookkeeping.

## Purpose

The node is the smallest reactive cell rimsky orchestrates. Cascade resolution propagates between nodes; claim acquisition is per-node; dispatch is per-node. Templates compose by declaring node-to-node dependencies and per-node policy.

## Boundaries

The node owns: its dispatch / terminal lifecycle, its claim spec list, its handler resolutions, its quality-rule evaluations, its attribute writeback. The node does **not** own: cascade scheduling (see `frame`), claim conflict resolution (see `claim-handle`), event-log shape (see `event-log`). Adjacent: `node-state`, `last-outcome`, `frame`, `cascade`, `attribute`, `lifecycle-handler`, `on-event-handler`, `claim`, `named-lock`.

## Invariants

- The set of legal `state` values is exactly `{fresh, stale, running, failed, parked}`; transitions follow `foundation/cascade/state.go::NextState`. Same-state transitions are rejected under `dispatch_claimed` (`@blessed-invariant 1`, also numbered §17).
- Eligibility for dispatch reads only `state`. Cascade propagation downstream reads only `last_outcome` (`@blessed-invariant`-adjacent rule, `docs/concepts/cascade.md`).
- A non-fresh `rimsky_nodes` row always carries a `frame_id`.

## Aliases and historical names

`graph-node` is an older spelling in early prose. The 4-state vocabulary (`fresh | stale | running | failed`) predates the addition of `parked` in migration 006 — older prose snippets sometimes still cite four states.

## Open within this concept

- The `4-vs-5 states` vocabulary drift across CLAUDE.md and `docs/concepts/node-state.md` (see `tensions/state-count-drift.md`).

## Notes

- 2026-05-14: `dependencies:` retires; `subscribes:` introduced (`see concept:subscription`); substitution refs auto-subscribe. The `on_event:` map retires; `concept:on-event-handler` is retired to `_retired/`. Lifecycle handlers lose their `invalidate.targets:` clauses. Per spec `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
