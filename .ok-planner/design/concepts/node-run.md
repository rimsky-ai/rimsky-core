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

Post-2026-05-15 the row also carries the run-tree extension and all state-bearing columns lifted from `rimsky_nodes`. Post-2026-05-20, `rimsky_node_attributes` is also per-run (foreign-key to this row via `node_run_id` with `ON DELETE CASCADE`), completing the lift — modulo derived caches, every state-bearing column for a node-run lives on this row or cascades from it. Post-2026-05-22, the parent/child relationship moves to `rimsky_run_scopes` (per `concept:run-scope`); inline `parent_run_id` + `child_key` columns are dropped, replaced by a non-null `run_scope_id` FK:

- `run_scope_id UUID NOT NULL` — FK to `rimsky_run_scopes` (per `concept:run-scope`). All scoping — parent/child relationship for fan-out, sub-graph membership for delegation — is now expressed through this FK chain rather than inline on the node_run row.
- `aggregation_policy JSONB NULL` — snapshotted from the template-node spec at run creation time; encodes the failure policy (`strict.cancel_siblings`, `threshold`, `best_effort`, `first`) for parent-run aggregation.
- `state TEXT NOT NULL` — `fresh | stale | running | failed | parked`. State lives entirely here now; the legacy `rimsky_nodes.state` column is removed.
- `last_outcome TEXT NOT NULL` — `fresh_changed | fresh_unchanged | passed | pure_cascade | failed`. Cascade-firing gate.
- `parked_reason TEXT NULL`, `parked_reason_label TEXT NULL`, `parked_resume_at TIMESTAMPTZ NULL` — parked-state taxonomy (see `concept:parked-state`).

## Purpose

One queryable lifecycle row per node-run means every cross-process question ("is this run still active?", "what stores does it need?", "which frame is it in?", "has it gone stale?") is a SQL predicate over indexed columns. The frame ⊃ node-run hierarchy is the model: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.

**Run-tree** (post-2026-05-22): node-runs are organized into RunScopes (per `concept:run-scope`) via `run_scope_id`. The tree shape that previously lived inline on the node_run row (the post-2026-05-15 `parent_run_id` + `child_key` columns) now lives on `rimsky_run_scopes` via `parent_run_scope_id`. Walking the RunScope tree from a leaf RunScope to the main RunScope recovers the full execution stack. A run represents the dispatch of one node within one RunScope; a fan-out parent's children live in fanout_partition RunScopes (one per partition); a sub-graph's internal nodes live in a sub-graph RunScope. Trees may be arbitrarily deep: fan-out of fan-outs, sub-graphs containing fan-outs, fan-outs of sub-graphs. State aggregation walks bottom-up through the RunScope tree (state propagation transaction at `runtime/state_propagation.go::PropagateChildState`).

## Boundaries

Owns: the node-run lifecycle column, candidate-selection inputs, heartbeat columns, park columns. Does NOT own: per-claim ledger rows (see `claim-handle`), per-holder subgraph state (see `claim-handle#held-variant`), node state (see `node-state`), the parent-child run relationship (now lives on `rimsky_run_scopes` per `concept:run-scope`). Adjacent: `claim-handle`, `frame`, `supervisor`, `parked-state`, `run-scope`.

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
- 2026-05-20 — Per-run attribute lift complete. `rimsky_node_attributes` re-keyed from `node_id` to `node_run_id` with cascade delete via the run row. The 2026-05-15 "all state-bearing columns" claim is now literally true (modulo derived caches). See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
- 2026-05-21 — Dispatch-row phase flip moved into `applyTerminalComplete`'s tx (between `UpdateState` and `cascadeSubscribersStaleInTx`), aligning with the in-tx flip every other terminal already did (`applyTerminalPass`, `applyErrorPolicy`, `applyTerminalInfraError`; `applyTerminalPark` via `ParkActiveInTx`). Outer `Queue.Complete` calls in `supervisor.go` and `callback.go` survive as belt-and-suspenders idempotent re-completion. This is the architectural change that makes `frame: in` self-subscriptions first-class (`MarkStaleForCascade`'s `NOT EXISTS (phase IN active set)` guard now passes for self-edges because runOld is terminal-phase by the time the cascade walk fires). Sits naturally inside the 2026-05-22 callback-determinism tx-passing refactor (apply* now takes the outer `tx` parameter). See `concept:node-subscription`.
- 2026-05-22 — Reshape per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`: `parent_run_id` and `child_key` removed from `rimsky_node_runs`; replaced by `run_scope_id` (FK to `rimsky_run_scopes`). Run-tree shape moves to `concept:run-scope`. The two partial-unique in-flight indexes (`uq_node_runs_in_flight_per_root_node`, `uq_node_runs_in_flight_per_child`) collapse to one keyed on `(node_id, run_scope_id)` (`uq_node_runs_in_flight_per_run_scope`).
