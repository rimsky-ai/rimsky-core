---
concept: node-run
status: as-is
aliases: []
---

# Node-run

## What it is

The node-run row is the parent row for one execution of one node within a frame. It carries `phase ∈ {pending, active, held, parked, completed}`, `claimed_by` (supervisor id, non-null only while `phase='active'`), a non-null `frame_id`, the required-stores list, and optional park fields (`parked_reason`, `parked_reason_label`, `parked_reason_note`, `resume_at`).

The row carries liveness and async-callback fields — `last_progress_at`, `async_ack_id`, `async_ack_registered_at`. `last_progress_at` is bumped by attribute writeback callbacks and by keepalive POSTs; `async_ack_id` is populated when the executor returns AwaitAsyncCallback and is consulted by the callback handler to correlate inbound POSTs to the dispatch row.

The row also carries a `prior_dispatch_id` nullable reference to a preceding dispatch row, set whenever a new dispatch is enqueued to follow a predecessor one (under any of the `prior_dispatch_id`-bearing dispositions — stale-recovery, retry-after-error, recalculate). Optional scratch fields — `scratch_inline`, `scratch_handle`, `scratch_handle_backend` — carry executor-attached opaque bytes per dispatch, with spill following `concept:blob-backend`. The executor sets scratch either by attaching scratch bytes to the settling terminal Outcome or mid-dispatch (by POSTing to the scratch HTTP callback route, paralleling the executor protocol's existing attributes incremental-writeback HTTP callback); both writes persist on the dispatch row that received them. When a subsequent dispatch row is created for the same node and the new row carries a non-null `prior_dispatch_id`, the enqueue path copies scratch from the predecessor dispatch row onto the new row at row creation, and the executor reads it from its own row on next dispatch.

A `tags` representation is populated from the settling terminal verdict; the storage form is the persistence driver's choice — array column or junction table.

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

Owns: the node-run lifecycle phase, candidate-selection inputs, liveness fields — `last_progress_at`, `async_ack_id`, `async_ack_registered_at` — park fields, the node-run's state, and last-outcome; executor-attached opaque scratch bytes per dispatch; the `prior_dispatch_id` linkage across re-dispatches of the same node. Does NOT own: per-claim ledger rows (see `claim-handle`), per-holder subgraph state (see `claim-handle`), the parent-child run relationship (lives on the run-scope record per `concept:run-scope`). Adjacent: `claim-handle`, `frame`, `supervisor`, `parked-state`, `run-scope`, `terminal-tag`.

## Invariants

- `frame_id` is NOT NULL — every node-run carries its frame (frames are the unit of cascade resolution).
- `claimed_by` is non-null only while `phase='active'`.
- Orphan reaper covers only `phase='active'` rows; parked rows skipped explicitly (settled with respect to liveness). For active rows the orphan signal is the supervisor's gRPC client connection state (sync dispatches) or quiet-period exceeded (`now - last_progress_at > max_quiet_period`) plus absolute-deadline exceeded (`now - dispatched_at > max_runtime`), each enforced only when the corresponding deadline is non-zero.
