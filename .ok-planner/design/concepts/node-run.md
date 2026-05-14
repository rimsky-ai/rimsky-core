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

`rimsky_node_runs` is the parent row for one execution of one node within a frame. Columns include `phase ∈ {pending, active, held, parked, completed}`, `claimed_by` (supervisor id, non-null only while `phase='active'`), `frame_id NOT NULL`, `last_heartbeat_at`, `required_stores`, optional park columns (`parked_at`, `resume_at`, `parked_payload_*`, `session_token`, `parked_reason`, `wake_reason`).

## Purpose

One queryable lifecycle row per node-run means every cross-process question ("is this run still active?", "what stores does it need?", "which frame is it in?", "has it gone stale?") is a SQL predicate over indexed columns. The frame ⊃ node-run hierarchy is the model: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.

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
