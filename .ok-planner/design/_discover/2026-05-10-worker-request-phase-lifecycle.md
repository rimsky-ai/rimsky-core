---
topic: worker-request-phase-lifecycle
kind: schema
---

# Unified `rimsky_worker_request` with `phase` column; held claim handles outlive parent active terminal via `ON DELETE SET NULL`

## Description

A node's dispatch lifecycle has multiple distinct stages: queued, actively claimed by a supervisor, held across multiple downstream nodes (held subgraph), parked awaiting external resume, and terminal. An older schema split these across separate tables (`rimsky_dispatch`, `rimsky_lock_holders`); the Phase-5 layer-crystallization consolidation turned them into one parent (`rimsky_worker_request`) plus one child (`rimsky_claim_handle`) keyed by phase.

`rimsky_worker_request` (`foundation/persistence/postgres/migrations/001-initial.sql:103-120`) carries a `phase` column with CHECK constraint `phase IN ('pending','active','held','parked','completed')`. Migration 006 added `'parked'`. `claimed_by` is non-null only while `phase='active'`. The orphan reaper covers only `phase='active'` rows (stale heartbeat); parked rows are explicitly skipped because parked nodes do not heartbeat.

`rimsky_claim_handle` is a child of `rimsky_worker_request` via FK with `ON DELETE SET NULL`, **not** `CASCADE` (migration 001 lines 173-177). The inline justification:

> Held claim handles outlive their owning worker-request's active-phase terminal until auto-terminal resolution... Cascade would race against held-claim resolution.

`@blessed-invariant 19` (`foundation/persistence/worker_requests.go:34`): dispatch rows always carry a non-zero `frame_id`. Frame-end is the SQL predicate "no `rimsky_nodes` rows in `stale` or `running` for this instance" (`2026-05-10-frame-resolution-model`).

The five phases:

- **`pending`** — created by the scheduler; awaiting candidate selection.
- **`active`** — `claimed_by` populated; supervisor is dispatching the executor or running a non-executor node.
- **`held`** — auto-terminal hasn't fired yet; the row stays to anchor the held subgraph.
- **`parked`** — the node emitted `ParkRequested`; waiting for resume.
- **`completed`** — terminal; sweeper retains briefly for audit/event correlation.

Adding a new lifecycle phase requires editing both the CHECK constraint (via a new migration) and every sweep that filters by phase. The current sweeps:

- Orphan claim reaper — `phase='active'` only.
- Sweep parked — `phase='parked'` with `resume_at < now()`.
- Auto-terminal — `phase='held'` with all `rimsky_claim_holders` non-active.

Per CLAUDE.md "Schema (post-Phase-5 layer-crystallization consolidation)":

- `rimsky_worker_request` replaces the legacy `rimsky_dispatch`.
- `rimsky_claim_handle` replaces the legacy `rimsky_lock_holders`. `lock_kind` ∈ `{'named','scope'}`. `is_held BOOLEAN` marks claims that persist past active terminal. `realized_write_semantics` is the per-claim verdict.
- `rimsky_claim_holders` is the held-claim subgraph state ledger. FK column is `claim_handle_id` (renamed from `lock_holder_id`).

The Phase-5 consolidation made every dispatch-state question answerable from one parent row plus one child table. Pre-consolidation, every question required cross-table joins; the held-claim outlive-active-terminal lifecycle was hard to express atomically.

## Code surface

- `foundation/persistence/postgres/migrations/001-initial.sql:5-37, 103-120, 170-209, 221-232` — schema with annotations.
- `foundation/persistence/postgres/migrations/006-platform-extensions-park-blob-events.sql:13-40` — `parked` phase + park columns.
- `foundation/persistence/worker_requests.go:14-100` — Go-side struct + `@blessed-invariant 19`.
- `foundation/persistence/claim_handles.go` — Go-side CRUD.
- `foundation/persistence/claim_holders.go` — Go-side CRUD.
- `foundation/integration/orphan_reaper.go` — phase='active' sweep.
- `foundation/integration/sweep_parked.go` — phase='parked' sweep.
- `foundation/integration/auto_terminal.go` — phase='held' resolution.

## Prose surface

- `CLAUDE.md` "Schema (post-Phase-5 layer-crystallization consolidation)".
- `CLAUDE.md` "Non-obvious gotchas" — "Worker-request phase column drives lifecycle."
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — schema in the contract.
- `.ok-planner/history/` — pre-Phase-5 schema designs (legacy `rimsky_dispatch` / `rimsky_lock_holders`).

## Adjacent topics

- `2026-05-10-auto-terminal-aggregate-resolution` — uses `phase='held'` rows.
- `2026-05-10-parked-state-and-resume` — `phase='parked'`.
- `2026-05-10-orphan-reaper-no-producer-abandon` — `phase='active'` only.
- `2026-05-10-frame-resolution-model` — `frame_id NOT NULL` invariant 19.
- `2026-05-10-claimant-guarded-release` — `claimed_by` predicates.

## Observations

- Five phases plus the implicit "deleted" terminal makes for six effective lifecycle stages. The CHECK constraint is the single source of truth for legal values; the Go-side enum mirrors it in `worker_requests.go`.
- `ON DELETE SET NULL` (vs `CASCADE`) on `rimsky_claim_handle.worker_request_id` is the specific schema choice that lets held handles outlive their parent. A future "really delete the parent" admin operation must arrange for the held handles to be resolved separately or they become orphan rows the periodic reaper picks up.
- The migration 006 prologue acknowledges the pre-v1 CHECK-constraint-recreate idiom: the constraint is dropped and recreated rather than `ALTER` for backward simplicity. This is consistent with `2026-05-10-pre-v1-break-freely-migrations`.
- `held` phase is distinct from "the row's auto-terminal hasn't fired yet" — they're the same concept, but the phase column is the queryable representation. Auto-terminal updates the phase to `completed` (and deletes the claim-handle) when all holders are non-active.
