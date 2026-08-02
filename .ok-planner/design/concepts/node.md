---
concept: node
aliases:
  - graph-node
---

# Node

## What it is

A node is one declarative unit of work in a template's graph. Each node has a name, a type (template-author chosen string used as the dispatch routing key), zero-or-more subscription entries declaring its reactive surface, zero-or-more required claims/locks, zero-or-more co-held-upstream-claim declarations, an optional attributes JSON schema, optional operator-facing tags, optional per-error-class policy chains, and (for non-claim-only nodes) a target executor. At runtime, a node materializes as a per-instance node row keyed by (instance, node-type), carrying identity fields, operator-facing tags, and cascade-mode. The node row carries NO runtime state — no lifecycle phase, no policy-evaluation cursor, no retry counter, no in-flight engagement marker. All execution state is per-run and lives on `concept:node-run`. Operator-facing surfaces project node-runs into a categorical summary with four counts: `active` (runs in any in-flight executing state — `running`, `held`, or `parked`), `pending` (runs in any in-flight pre-dispatch state — `pending` or `stale`), `fresh` (runs settled successfully), and `failed` (runs settled with terminal error). Each node-run contributes to exactly one count; the seven-state taxonomy in `concept:node-run` partitions cleanly into these four buckets. The per-run terminal disposition lives on the node-run's settling-signal-type field (see `concept:node-run`).

## Purpose

The node is the smallest reactive cell rimsky orchestrates. Cascade resolution propagates between nodes; claim acquisition is per-node; dispatch is per-node. Templates compose by declaring node-to-node dependencies and per-node policy.

## Boundaries

The node owns: its dispatch / terminal lifecycle, its claim spec list, its per-error-class policy chains, its attribute writeback, its operator-facing tags. The node does **not** own: cascade scheduling (see `frame`), claim conflict resolution (see `claim-handle`), event-log shape (see `event-log`). Adjacent: `signal`, `error-policy`, `frame`, `cascade`, `attribute`, `claim`, `named-lock`, `node-subscription`, `node-run`, `message-sender-node`.

## Invariants

- Nodes carry no runtime state. The node row holds identity, tags, and template-derived cascade-mode only; lifecycle state, policy-evaluation cursor, retry counter, and in-flight engagement live on `concept:node-run`. Operator-facing surfaces synthesize categorical run counts from the node-run rows on demand.
- **Node rows are immutable during frame processing.** The `rimsky_nodes` row is set once at instance creation and never updated thereafter; the only later lifecycle action touching it is deletion at instance-terminate. Node-reset via the control API is a pure retry-budget clear that operates entirely on the failed-terminal `concept:node-run` row — it never writes to `rimsky_nodes`. No cascade walker, gate evaluator, dispatcher, terminal handler, cascade-mode rule, or signal emitter — nothing that runs inside a frame — writes to the `rimsky_nodes` row. Every piece of state that changes during frame processing lives on `rimsky_node_runs` and `rimsky_node_attributes`, which are per-run rows created inside the frame that dispatches them.
- Eligibility for dispatch reads the node-run's state (see `concept:node-run`). Cascade propagation is subscriber-driven via `concept:signal`: a subscription edge fires iff its signal type-path pattern matches the emitted signal AND its compiled payload predicate evaluates true against the signal payload.
- Tag values admit instance-parameter substitution at materialization time (instance creation); no other substitution source kinds are available at that phase. Tag substitution failures are fatal at instance creation, matching the dispatch-time discipline for required-attribute substitution. Tags do not gate dispatch, cascade, or validation — they are operator-facing metadata.

## Kind sugar

A template node may declare a kind-alias as a shorthand for an executor reference. The node's type field (the template-author-chosen dispatch routing key) is unchanged and unrelated. At registration, a static kind-alias map resolves the alias to a pre-registered executor entry. A node may declare a kind-alias or an executor reference but not both; mixing is rejected at registration. Unknown kind-aliases are rejected the same way unknown executors are. The sugar exists so utility nodes (counters, gates, and similar in-process executors) can be referenced without spelling out their executor identity.
