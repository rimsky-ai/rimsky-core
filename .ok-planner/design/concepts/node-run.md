---
concept: node-run
status: as-is
aliases:
  - worker-request (legacy)
  - dispatch (legacy)
references:
  - _discover/2026-05-10-worker-request-phase-lifecycle.md
  - _discover/2026-05-10-supervisor-acceptance-lists.md
  - _discover/orphan-claim-cutoff-five-heartbeats.md
  - _discover/2026-05-10-parked-state-and-resume.md
---

# Node-run

## What it is

`rimsky_node_runs` is the parent row for one execution of one node within a frame. Columns include `phase ∈ {pending, active, held, parked, completed}`, `claimed_by` (supervisor id, non-null only while `phase='active'`), `frame_id NOT NULL`, `last_heartbeat_at`, `required_stores`, optional park columns (`parked_at`, `resume_at`, `parked_payload_*`, `session_token`, `parked_reason`, `parked_reason_label`, `wake_reason`).

Post-2026-05-15 the row also carries the run-tree extension and all state-bearing columns lifted from `rimsky_nodes`:

- `parent_run_id UUID NULL` — FK self. NULL for top-level (root) runs.
- `child_key TEXT NULL` — child identity within parent's namespace (partition key for fan-out children; internal node's alias for sub-graph internal nodes).
- `aggregation_policy JSONB NULL` — snapshotted from the template-node spec at run creation time; encodes the failure policy (`strict.cancel_siblings`, `threshold`, `best_effort`, `first`) for parent-run aggregation.
- `state TEXT NOT NULL` — `fresh | stale | running | failed | parked`. State lives entirely here now; the legacy `rimsky_nodes.state` column is removed.
- `last_outcome TEXT NOT NULL` — `fresh_changed | fresh_unchanged | passed | pure_cascade | failed`. Cascade-firing gate.
- `parked_reason TEXT NULL`, `parked_reason_label TEXT NULL`, `parked_resume_at TIMESTAMPTZ NULL` — parked-state taxonomy (see `concept:parked-state`).

## Purpose

One queryable lifecycle row per node-run means every cross-process question ("is this run still active?", "what stores does it need?", "which frame is it in?", "has it gone stale?") is a SQL predicate over indexed columns. The frame ⊃ node-run hierarchy is the model: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.

**Run-tree** (post-2026-05-15): node-runs form a tree via `parent_run_id` + `child_key`. A root run has both columns NULL and represents the dispatch of one outer-graph node within a frame. A child run represents a fan-out work unit or a sub-graph internal node's run. Trees may be arbitrarily deep: fan-out of fan-outs, sub-graphs containing fan-outs, fan-outs of sub-graphs. Every node has at least one run per frame in which it's stale-marked; a non-fan-out, non-delegating node has exactly one run (no children). State aggregation walks bottom-up (state propagation transaction at `runtime/state_propagation.go::PropagateChildState`).

## Boundaries

Owns: the node-run lifecycle column, candidate-selection inputs, heartbeat columns, park columns. Does NOT own: per-claim ledger rows (see `claim-handle`), per-holder subgraph state (see `claim-handle#held-variant`), node state (see `node-state`). Adjacent: `claim-handle`, `frame`, `supervisor`, `parked-state`.

## Invariants

- `frame_id` is NOT NULL — every node-run carries its frame (CLAUDE.md "Frames are the unit of cascade resolution" gotcha).
- `claimed_by` is non-null only while `phase='active'`.
- Orphan reaper covers only `phase='active'` rows; parked rows skipped explicitly (they don't heartbeat).
- Heartbeat cutoff is `5 × heartbeat_interval` (`@blessed-invariant 6`), same as claim-handle.

## Aliases and historical names

Renamed from `concept:worker-request` per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #14). The legacy table names were `rimsky_dispatch` (pre-Phase-5) and `rimsky_worker_request` (Phase-5 through 2026-05-12). The current Go-side struct is `NodeRunRow`. Some prose still uses "dispatch row" as a colloquial term.

## Open within this concept

- Five-phase CHECK + Go enum is the single source of truth; new phases require coordinated migration + sweep updates (no specific tension; just discipline).

## Notes

- Renamed from `concept:worker-request` per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #14).
