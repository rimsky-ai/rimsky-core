---
concept: node
status: as-is
aliases:
  - graph-node
---

# Node

## What it is

A node is one declarative unit of work in a template's graph. Each node has a name, a type (template-author chosen string used as the dispatch routing key), zero-or-more subscription entries declaring its reactive surface, zero-or-more required claims/locks, zero-or-more co-held-upstream-claim declarations, an optional attributes JSON schema, optional operator-facing tags, optional per-error-class policy chains, and (for non-claim-only nodes) a target executor. At runtime, a node materializes as a per-instance node row keyed by instance and node-type, carrying its state, frame reference, tags, and per-node bookkeeping; per-run terminal disposition lives on the node-run's settling-signal-type field (see `concept:node-run`).

## Purpose

The node is the smallest reactive cell rimsky orchestrates. Cascade resolution propagates between nodes; claim acquisition is per-node; dispatch is per-node. Templates compose by declaring node-to-node dependencies and per-node policy.

## Boundaries

The node owns: its dispatch / terminal lifecycle, its claim spec list, its per-error-class policy chains, its attribute writeback, its operator-facing tags. The node does **not** own: cascade scheduling (see `frame`), claim conflict resolution (see `claim-handle`), event-log shape (see `event-log`). Adjacent: `signal`, `error-policy`, `frame`, `cascade`, `attribute`, `claim`, `named-lock`, `node-subscription`, `node-run`.

## Invariants

- The legal state values are exactly the node's lifecycle states; transitions follow the foundation state-machine's next-state function. Same-state transitions are rejected under `dispatch_claimed` (invariant 1).
- Eligibility for dispatch reads only the node's state. Cascade propagation is subscriber-driven via `concept:signal`: a subscription edge fires iff its signal type-path pattern matches the emitted signal AND its compiled payload predicate evaluates true against the signal payload.
- A non-fresh node row always carries a frame reference.
- Tag values admit instance-parameter substitution at materialization time (instance creation); no other substitution source kinds are available at that phase. Tag substitution failures are fatal at instance creation, matching the dispatch-time discipline for required-attribute substitution. Tags do not gate dispatch, cascade, or validation — they are operator-facing metadata.

## Kind sugar

A template node may declare a kind-alias as a shorthand for an executor reference. The node's type field (the template-author-chosen dispatch routing key) is unchanged and unrelated. At registration, a static kind-alias map resolves the alias to a pre-registered executor entry. A node may declare a kind-alias or an executor reference but not both; mixing is rejected at registration. Unknown kind-aliases are rejected the same way unknown executors are. The sugar exists so utility nodes (counters, gates, and similar in-process executors) can be referenced without spelling out their executor identity.
