---
concept: node-run
---

# Node-run

## What it is

A node-run is the record of one execution of one node inside one frame. A single state carries the run's whole lifecycle, from the moment rimsky creates it, through the moment a supervisor dispatches it, to the moment it settles. A run settles either successfully or with a terminal error, and a settled run never leaves that state. Between dispatch and settlement a run may hold a claim or park, and rimsky counts both as in flight.

The record carries the run's identity and its place in the graph: the frame it belongs to, the run scope that positions it in the run tree (see `concept:run-scope`), the producers whose claims it needs, and the supervisor that holds it while it is dispatched. A sequence, assigned when rimsky creates the row, orders the runs of one node within one run scope. A creation reason, also recorded at creation, says why the row exists; it governs whether the row joins the cascade walker's accumulation, whether it waits on upstream before it becomes eligible, and whether the template's cascade mode applies to it.

The record carries the run's progress. An acknowledgement identity is recorded when an executor answers that it will call back later, and the callback handler reads that identity to match an inbound notification to this run. Park timing records when the run parked and when it resumes (see `concept:parked-state`). A retry count holds where error-policy evaluation has reached on this dispatch (see `concept:error-policy`, `decision:in-place-retry`). A settling signal records the signal the run settled with; cascade fires from the emitted signal itself rather than from that record (see `concept:signal`). Tags on the run follow from the settling verdict.

The record carries what the executor attaches. Scratch is opaque bytes the executor hands back with a settling outcome — there is no channel for writing scratch mid-dispatch (see `decision:scratch-protocol`) — and persists as a byte column on the row (see `decision:scratch-column`). When rimsky creates a further run of the same node that supersedes this one, it copies the scratch forward onto the new row, and the executor reads it from its own row on the next dispatch. That new row also references the dispatch it supersedes and records why it supersedes it; the reason persists on the row, so rimsky re-sends it to the executor on the next dispatch even after a supervisor restarts. A fan-out parent additionally carries a snapshot of the aggregation policy, taken from the node's fan-out error policy when the parent dispatches its children.

The node-run holds every state-bearing field for the run. Per-node attributes are a child record keyed to the run and deleted with it (see `concept:attribute`), so every such field either lives on the run or hangs off it.

## Purpose

One record per node-run makes every cross-process question a single query: whether a run is still active, which producers it needs, which frame it belongs to, whether it has gone stale. The frame is one run of the cascade (see `concept:frame`), and the node-run is one node's execution inside that frame.

The run tree follows from the run scope each run references. Walking that tree from a leaf scope to the frame's root scope recovers the whole execution stack for the frame: a fan-out parent's children sit in one scope per partition, and a sub-graph's internal nodes sit in the sub-graph's own scope. The nesting has no fixed depth — a fan-out of fan-outs, a sub-graph holding a fan-out, a fan-out of sub-graphs. Rimsky aggregates state upward through that tree in one transaction, and a parent's verdict follows its children: when a child transitions late, rimsky re-projects the parent's state and its settling signal from the children, even after the parent has settled.

## Boundaries

A node-run owns its lifecycle state, the fields a dispatcher reads to select candidates, its progress and acknowledgement fields, its park timing, its sequence, its creation reason, the scratch an executor attaches to it, and its reference to the dispatch it supersedes. It does not own the claim ledger or the per-holder subgraph state on a claim (see `claim-handle`). It does not own the parent-child relationship between runs, which the run scope carries (see `run-scope`). It does not own the cascade walker's choice to accumulate or to queue (see `cascade`), and it does not own the gate that turns a waiting run into an eligible one (see `wait-set`). The dispatcher's serialization gate belongs to the dispatcher; the node-run anchors it only by defining which states count as in flight. See also: `frame`, `supervisor`, `parked-state`, `terminal-tag`, `node`.
