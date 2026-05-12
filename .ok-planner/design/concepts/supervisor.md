---
concept: supervisor
status: as-is
aliases: []
references:
  - _discover/2026-05-10-supervisor-acceptance-lists.md
  - _discover/2026-05-10-verify-before-run-guard.md
  - _discover/2026-05-10-claimant-guarded-release.md
  - _discover/2026-05-10-atomic-acquisition-decoupled-tx.md
  - _discover/2026-05-10-postgres-only-runtime-state.md
---

# Supervisor

## What it is

One of the three rimsky runtime binaries (`cmd/rimsky-supervisor/`). Implements the acquisition transaction, dispatch, terminal handling, auto-terminal. Registers in `rimsky_supervisors` at startup with `accepted_executors` / `accepted_stores` / `concurrency` / `callback_host` / `callback_port`. Heartbeats are queryable timestamps on `rimsky_worker_request` and `rimsky_claim_handle`.

## Purpose

The supervisor is rimsky's worker side. It selects candidate work, performs the atomic acquisition transaction, calls executor `Execute`, handles terminal events, fires auto-terminal verbs. Multiple supervisors run concurrently and coordinate only through Postgres.

## Boundaries

Owns: the acquisition tx, the dispatch call, terminal-handler resolution, callback HTTP server, heartbeating. Does NOT own: scheduling (see `schedule`), control-plane (see `control-api`), claim-state mutation outside the tx (see `claim-producer`). Adjacent: `worker-request`, `claim-handle`, `executor`, `frame`, `lifecycle-handler`, `auto-terminal`.

## Invariants

- All claim-handle mutations and claim releases by this supervisor carry `AND holder_supervisor_id = supervisor_id` (`@blessed-invariant 4`).
- Verify-before-run: after the acquisition tx commits, re-read `claimed_by` and bail as `orphaned_claim_lost_race` if ownership moved (`@blessed-invariant 5`).
- Acquisition transaction is rimsky-side atomic; `ClaimProducer.Open` runs in its own decoupled tx (`@blessed-invariant 10`).
- `Open` fires inside the rimsky-side acquisition transaction (`@blessed-invariant 15`).
- `accepted_executors` / `accepted_stores` filter candidate selection: `required_stores <@ :accepted_stores` (Postgres array-contained-in).
- Two distinct callback hostnames: binds on `0.0.0.0`; advertises via `callback.advertise_host`.

## Aliases and historical names

The supervisor's role was once split differently pre-phase-5; the unified runner under `foundation/integration/` is the current home.

## Open within this concept

(no live tensions distinct from `claim-handle`, `worker-request`, and the verify-before-run / acquisition-tx invariants)

