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

A node is one declarative unit of work in a template's graph. Each node has a name, a type (template-author chosen string used as the dispatch routing key), zero-or-more `subscribes:` entries declaring its reactive surface, zero-or-more required claims/locks, zero-or-more `holds:` declarations for co-held upstream claims, optional attributes JSON schema, optional operator-facing `tags`, optional `error_types:` per-error-class policy chains, and (for non-claim-only nodes) a target executor. At runtime, a node materializes as a per-instance node row (keyed by instance + node-type) carrying `state`, `frame_id`, `tags`, and per-node bookkeeping; per-run terminal disposition lives on the node-run's settling-signal-type field (see `concept:node-run`).

## Purpose

The node is the smallest reactive cell rimsky orchestrates. Cascade resolution propagates between nodes; claim acquisition is per-node; dispatch is per-node. Templates compose by declaring node-to-node dependencies and per-node policy.

## Boundaries

The node owns: its dispatch / terminal lifecycle, its claim spec list, its `error_types:` policy chains, its attribute writeback, its operator-facing tags. The node does **not** own: cascade scheduling (see `frame`), claim conflict resolution (see `claim-handle`), event-log shape (see `event-log`). Adjacent: `signal`, `error-policy`, `frame`, `cascade`, `attribute`, `claim`, `named-lock`, `node-subscription`, `node-run`.

## Invariants

- The set of legal `state` values is exactly `{fresh, stale, running, failed, parked}`; transitions follow the foundation state-machine's next-state function. Same-state transitions are rejected under `dispatch_claimed` (`@blessed-invariant 1`, also numbered §17).
- Eligibility for dispatch reads only `state`. Cascade propagation is subscriber-driven via `concept:signal`: a subscription edge fires iff its signal type-path pattern matches the emitted signal AND its compiled CEL `when:` predicate evaluates true against the signal payload (the pre-2026-05-23 sender-side `last_outcome` gate retired with the canonical signal taxonomy).
- A non-fresh node row always carries a `frame_id`.
- Tag values admit `{{params.<key>}}` substitution at materialization time (instance creation); no other substitution source kinds are available at that phase. Tag substitution failures are fatal at instance creation, matching the dispatch-time discipline for required-attribute substitution. Tags do not gate dispatch, cascade, or validation — they are operator-facing metadata.

## Aliases and historical names

`graph-node` is an older spelling in early prose. The 4-state vocabulary (`fresh | stale | running | failed`) predates the addition of `parked` in migration 006 — older prose snippets sometimes still cite four states.

## Open within this concept

- The 4-vs-5 states vocabulary drift between older prose (which still cites four states) and the current five-state set (see `tension:state-count-drift`).

## Notes

- 2026-05-14: `dependencies:` retires; `subscribes:` introduced (see `concept:node-subscription`); substitution refs auto-subscribe. The `on_event:` map retires; the former on-event-handler concept is retired. Lifecycle handlers lose their `invalidate.targets:` clauses. Per `spec:2026-05-14-subscription-cascade-and-quality-of-life-design`.
- 2026-05-19 — Tags added per `spec:2026-05-19-multi-instance-template-ergonomics-design`. Pre-existing drift cleaned up in same pass: dropped retired `on_event`/`quality_rules` from "What it is", dropped `its quality-rule evaluations` from Boundaries, dropped the retired node-state/on-event-handler entries from the Adjacent list.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
