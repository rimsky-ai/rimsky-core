---
concept: node-run
status: as-is
aliases:
  - worker-request (legacy)
  - dispatch (legacy)
---

# Node-run

## What it is

The node-run row is the parent row for one execution of one node within a frame. It carries `phase ∈ {pending, active, held, parked, completed}`, `claimed_by` (supervisor id, non-null only while `phase='active'`), a non-null `frame_id`, a last-heartbeat timestamp, the required-stores list, and optional park fields (parked-at, resume-at, parked-payload, session token, parked reason, parked reason label, wake reason).

Post-2026-05-15 the row also carries the run-tree extension and all state-bearing fields lifted from the per-instance node row. Post-2026-05-20, per-node attributes are also per-run (a child record keyed to this row, cascade-deleted with it), completing the lift — modulo derived caches, every state-bearing field for a node-run lives on this row or cascades from it. Post-2026-05-22, the parent/child relationship moves to the run-scope record (per `concept:run-scope`); the inline parent-run-id + child-key fields are dropped, replaced by a non-null run-scope reference:

- A non-null run-scope reference (per `concept:run-scope`). All scoping — parent/child relationship for fan-out, sub-graph membership for delegation — is now expressed through this reference chain rather than inline on the node-run row.
- An aggregation-policy field — snapshotted from the template-node spec at run creation time; encodes the failure policy (`strict.cancel_siblings`, `threshold`, `best_effort`, `first`) for parent-run aggregation.
- A `state` field — `fresh | stale | running | failed | parked`. State lives entirely here now; the legacy per-instance node-row state field is removed.
- A `last_outcome` field — `fresh_changed | fresh_unchanged | passed | pure_cascade | failed`. Cascade-firing gate.
- Parked reason, parked reason label, and parked resume-at fields — parked-state taxonomy (see `concept:parked-state`).

## Purpose

One queryable lifecycle row per node-run means every cross-process question ("is this run still active?", "what stores does it need?", "which frame is it in?", "has it gone stale?") is a SQL predicate over indexed columns. The frame ⊃ node-run hierarchy is the model: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.

**Run-tree** (post-2026-05-22): node-runs are organized into RunScopes (per `concept:run-scope`) via the run-scope reference. The tree shape that previously lived inline on the node-run row (the post-2026-05-15 parent-run-id + child-key fields) now lives on the run-scope record via its parent-run-scope reference. Walking the RunScope tree from a leaf RunScope to the main RunScope recovers the full execution stack. A run represents the dispatch of one node within one RunScope; a fan-out parent's children live in fanout-partition RunScopes (one per partition); a sub-graph's internal nodes live in a sub-graph RunScope. Trees may be arbitrarily deep: fan-out of fan-outs, sub-graphs containing fan-outs, fan-outs of sub-graphs. State aggregation walks bottom-up through the RunScope tree in a single state-propagation transaction.

## Boundaries

Owns: the node-run lifecycle phase, candidate-selection inputs, heartbeat fields, park fields, and (post-lift) the node-run's state and last-outcome. Does NOT own: per-claim ledger rows (see `claim-handle`), per-holder subgraph state (see `claim-handle`), the parent-child run relationship (now lives on the run-scope record per `concept:run-scope`). Adjacent: `claim-handle`, `frame`, `supervisor`, `parked-state`, `run-scope`.

## Invariants

- `frame_id` is NOT NULL — every node-run carries its frame (frames are the unit of cascade resolution).
- `claimed_by` is non-null only while `phase='active'`.
- Orphan reaper covers only `phase='active'` rows; parked rows skipped explicitly (they don't heartbeat).
- Heartbeat cutoff is `5 × heartbeat_interval` (`@blessed-invariant 6`), same as claim-handle.

## Aliases and historical names

Renamed from the former worker-request concept per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #14). The legacy persisted names were "dispatch" (pre-Phase-5) and "worker request" (Phase-5 through 2026-05-12). Some prose still uses "dispatch row" as a colloquial term.

## Open within this concept

- Five-phase CHECK + Go enum is the single source of truth; new phases require coordinated migration + sweep updates (no specific tension; just discipline).

## Notes

- Renamed from the former worker-request concept per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #14).
- 2026-05-20 — Per-run attribute lift complete. Per-node attributes re-keyed from node-id to node-run-id with cascade delete via the run row. The 2026-05-15 "all state-bearing columns" claim is now literally true (modulo derived caches). See `spec:2026-05-20-attribute-pull-resolution-design`.
- 2026-05-21 — Dispatch-row phase flip moved into the terminal-complete handler's transaction (between the state update and the subscriber-cascade stale-mark), aligning with the in-tx flip every other terminal already did (terminal-pass, error-policy, terminal-infra-error; terminal-park via its in-tx park call). The outer queue-complete calls in the supervisor and callback paths survive as belt-and-suspenders idempotent re-completion. This is the architectural change that makes `frame: in` self-subscriptions first-class (the cascade stale-mark's "no active in-flight run" guard now passes for self-edges because the old run is terminal-phase by the time the cascade walk fires). Sits naturally inside the 2026-05-22 callback-determinism transaction-passing refactor (the terminal handlers now take the outer transaction as a parameter). See `concept:node-subscription`.
- 2026-05-22 — Reshape per `spec:2026-05-22-fan-out-safety-scope-first-design`: the inline parent-run-id and child-key fields removed from the node-run row; replaced by a run-scope reference. Run-tree shape moves to `concept:run-scope`. The two partial-unique in-flight indexes (per-root-node and per-child) collapse to one keyed on node + run-scope.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
