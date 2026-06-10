---
concept: node
status: as-is
aliases:
  - graph-node
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
- Eligibility for dispatch reads only `state`. Cascade propagation is subscriber-driven via `concept:signal`: a subscription edge fires iff its signal type-path pattern matches the emitted signal AND its compiled CEL `when:` predicate evaluates true against the signal payload.
- A non-fresh node row always carries a `frame_id`.
- Tag values admit `{{params.<key>}}` substitution at materialization time (instance creation); no other substitution source kinds are available at that phase. Tag substitution failures are fatal at instance creation, matching the dispatch-time discipline for required-attribute substitution. Tags do not gate dispatch, cascade, or validation — they are operator-facing metadata.
