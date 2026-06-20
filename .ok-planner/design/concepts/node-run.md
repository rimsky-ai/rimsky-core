---
concept: node-run
status: as-is
aliases: []
---

# Node-run

## What it is

The node-run row is the parent row for one execution of one node within a frame. It carries a lifecycle phase across the dispatch arc (pending, active, held, parked, completed), a supervisor binding populated only while the run is actively dispatched, a non-null frame reference, the required-stores list, and the parked-reason metadata when applicable.

The row carries liveness and async-callback fields covering the run's last-progress timestamp and the async-acknowledgement identity. The progress timestamp is bumped by attribute writeback callbacks and by keepalive notifications; the async-acknowledgement identity is populated when the executor returns an await-callback outcome and is consulted by the callback handler to correlate inbound notifications back to the dispatch row.

The row also carries a nullable reference to a preceding dispatch row, set whenever a new dispatch is enqueued to follow a predecessor one (under any of the predecessor-bearing dispositions — stale-recovery, retry-after-error, recalculate). Optional scratch fields carry executor-attached opaque bytes per dispatch, with spill following `concept:blob-backend`. The executor sets scratch either by attaching scratch bytes to the settling terminal outcome or mid-dispatch via a scratch-callback notification, paralleling the executor protocol's incremental attribute-writeback callback; both writes persist on the dispatch row that received them. When a subsequent dispatch row is created for the same node and the new row carries a non-null predecessor reference, the enqueue path copies scratch from the predecessor dispatch row onto the new row at row creation, and the executor reads it from its own row on next dispatch.

A tags representation is populated from the settling terminal verdict.

The row carries the run-tree extension and all state-bearing fields for the node-run. Per-node attributes are a child record keyed to this row and cascade-deleted with it; modulo derived caches, every state-bearing field for a node-run lives on this row or cascades from it. The parent/child relationship lives on the run-scope record (per `concept:run-scope`), referenced from a non-null run-scope field on the row:

- A non-null run-scope reference (per `concept:run-scope`). All scoping — parent/child relationship for fan-out, sub-graph membership for delegation — is expressed through this reference chain.
- An aggregation-policy snapshot — snapshotted from the template-node spec at run creation time; encodes the failure policy for parent-run aggregation.
- A state field — the node-run's lifecycle state lives entirely here.
- A last-outcome field — the gate for cascade-firing.
- Parked-reason metadata — parked-state taxonomy (see `concept:parked-state`).

## Purpose

One queryable lifecycle row per node-run means every cross-process question ("is this run still active?", "what stores does it need?", "which frame is it in?", "has it gone stale?") is a SQL predicate over indexed columns. The frame ⊃ node-run hierarchy is the model: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.

**Run-tree**: node-runs are organized into RunScopes (per `concept:run-scope`) via the run-scope reference. The tree shape lives on the run-scope record via its parent-run-scope reference. Walking the RunScope tree from a leaf RunScope to the main RunScope recovers the full execution stack. A run represents the dispatch of one node within one RunScope; a fan-out parent's children live in fanout-partition RunScopes (one per partition); a sub-graph's internal nodes live in a sub-graph RunScope. Trees may be arbitrarily deep: fan-out of fan-outs, sub-graphs containing fan-outs, fan-outs of sub-graphs. State aggregation walks bottom-up through the RunScope tree in a single state-propagation transaction.

## Boundaries

Owns: the node-run lifecycle phase, candidate-selection inputs, liveness fields (the run's last-progress timestamp and the async-acknowledgement identity), park fields, the node-run's state, and last-outcome; executor-attached opaque scratch bytes per dispatch; the predecessor-dispatch linkage across re-dispatches of the same node. Does NOT own: per-claim ledger rows (see `claim-handle`), per-holder subgraph state (see `claim-handle`), the parent-child run relationship (lives on the run-scope record per `concept:run-scope`). Adjacent: `claim-handle`, `frame`, `supervisor`, `parked-state`, `run-scope`, `terminal-tag`.

## Invariants

- The frame reference is non-null — every node-run carries its frame (frames are the unit of cascade resolution).
- The supervisor binding is non-null only while the run is actively dispatched.
- Orphan reaper covers only actively-dispatched rows; parked rows skipped explicitly (settled with respect to liveness). For active rows the orphan signal is the supervisor's dispatch-channel connection state (sync dispatches) or quiet-period exceeded plus absolute-deadline exceeded, each enforced only when the corresponding deadline is non-zero.
