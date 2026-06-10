---
concept: node-run
status: as-is
aliases: []
---

# Node-run

## What it is

The node-run row is the parent row for one execution of one node within a frame. It carries `phase ∈ {pending, active, held, parked, completed}`, `claimed_by` (supervisor id, non-null only while `phase='active'`), a non-null `frame_id`, a last-heartbeat timestamp, the required-stores list, and optional park fields (parked-at, resume-at, parked-payload, session token, parked reason, parked reason label, wake reason).

The row carries the run-tree extension and all state-bearing fields for the node-run. Per-node attributes are a child record keyed to this row and cascade-deleted with it; modulo derived caches, every state-bearing field for a node-run lives on this row or cascades from it. The parent/child relationship lives on the run-scope record (per `concept:run-scope`), referenced from a non-null run-scope field on the row:

- A non-null run-scope reference (per `concept:run-scope`). All scoping — parent/child relationship for fan-out, sub-graph membership for delegation — is expressed through this reference chain.
- An aggregation-policy field — snapshotted from the template-node spec at run creation time; encodes the failure policy (`strict.cancel_siblings`, `threshold`, `best_effort`, `first`) for parent-run aggregation.
- A `state` field — `fresh | stale | running | failed | parked`. State lives entirely here.
- A `last_outcome` field — `fresh_changed | fresh_unchanged | passed | pure_cascade | failed`. Cascade-firing gate.
- Parked reason, parked reason label, and parked resume-at fields — parked-state taxonomy (see `concept:parked-state`).

## Purpose

One queryable lifecycle row per node-run means every cross-process question ("is this run still active?", "what stores does it need?", "which frame is it in?", "has it gone stale?") is a SQL predicate over indexed columns. The frame ⊃ node-run hierarchy is the model: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.

**Run-tree**: node-runs are organized into RunScopes (per `concept:run-scope`) via the run-scope reference. The tree shape lives on the run-scope record via its parent-run-scope reference. Walking the RunScope tree from a leaf RunScope to the main RunScope recovers the full execution stack. A run represents the dispatch of one node within one RunScope; a fan-out parent's children live in fanout-partition RunScopes (one per partition); a sub-graph's internal nodes live in a sub-graph RunScope. Trees may be arbitrarily deep: fan-out of fan-outs, sub-graphs containing fan-outs, fan-outs of sub-graphs. State aggregation walks bottom-up through the RunScope tree in a single state-propagation transaction.

## Boundaries

Owns: the node-run lifecycle phase, candidate-selection inputs, heartbeat fields, park fields, the node-run's state, and last-outcome. Does NOT own: per-claim ledger rows (see `claim-handle`), per-holder subgraph state (see `claim-handle`), the parent-child run relationship (lives on the run-scope record per `concept:run-scope`). Adjacent: `claim-handle`, `frame`, `supervisor`, `parked-state`, `run-scope`.

## Invariants

- `frame_id` is NOT NULL — every node-run carries its frame (frames are the unit of cascade resolution).
- `claimed_by` is non-null only while `phase='active'`.
- Orphan reaper covers only `phase='active'` rows; parked rows skipped explicitly (they don't heartbeat).
- Heartbeat cutoff is `5 × heartbeat_interval` (`@blessed-invariant 6`), same as claim-handle.
