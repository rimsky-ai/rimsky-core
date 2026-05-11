---
concept: worker-request
status: as-is
aliases:
  - dispatch (legacy)
references:
  - _discover/2026-05-10-worker-request-phase-lifecycle.md
  - _discover/2026-05-10-supervisor-acceptance-lists.md
  - _discover/orphan-claim-cutoff-five-heartbeats.md
  - _discover/2026-05-10-parked-state-and-resume.md
---

# Worker request

## What it is

`rimsky_worker_request` is the parent row for one dispatched run of one node. Replaces the legacy `rimsky_dispatch` table. Columns include `phase ∈ {pending, active, held, parked, completed}`, `claimed_by` (supervisor id, non-null only while `phase='active'`), `frame_id NOT NULL`, `last_heartbeat_at`, `required_stores`, optional park columns (`parked_at`, `resume_at`, `parked_payload_*`, `session_token`, `parked_reason`, `wake_reason`).

## Purpose

One queryable lifecycle row per dispatch means every cross-process question ("is this run still active?", "what stores does it need?", "which frame is it in?", "has it gone stale?") is a SQL predicate over indexed columns. Phase-5 consolidation merged what used to require cross-table joins.

## Boundaries

Owns: the dispatch lifecycle column, candidate-selection inputs, heartbeat columns, park columns. Does NOT own: per-claim ledger rows (see `claim-handle`), per-holder subgraph state (see `held-claim`), node state (see `node-state`). Adjacent: `claim-handle`, `frame`, `supervisor`, `parked-state`, `held-claim`.

## Invariants

- `frame_id` is NOT NULL — every dispatched row carries its frame (CLAUDE.md "Frames are the unit of cascade resolution" gotcha).
- `claimed_by` is non-null only while `phase='active'`.
- Orphan reaper covers only `phase='active'` rows; parked rows skipped explicitly (they don't heartbeat).
- Heartbeat cutoff is `5 × heartbeat_interval` (`@blessed-invariant 6`), same as claim-handle.

## Aliases and historical names

The legacy table name was `rimsky_dispatch`. The current Go-side struct is `WorkerRequest`. Some prose still uses "dispatch row" as a colloquial term.

## Open within this concept

- Five-phase CHECK + Go enum is the single source of truth; new phases require coordinated migration + sweep updates (no specific tension; just discipline).
- Heartbeat cutoff asymmetry between worker-request and claim-handle representations — see `tensions/heartbeat-cutoff-asymmetry.md`.
- Legacy table name `rimsky_dispatch` still surfaces in older prose and sketches despite the Phase-5 rename — see `tensions/lock-holder-vs-claim-handle-legacy.md`.

