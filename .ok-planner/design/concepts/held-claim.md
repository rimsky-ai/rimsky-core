---
concept: held-claim
status: as-is
aliases:
  - holding-subgraph
references:
  - _discover/2026-05-10-auto-terminal-aggregate-resolution.md
  - _discover/2026-05-10-worker-request-phase-lifecycle.md
  - _discover/2026-05-10-parked-state-and-resume.md
---

# Held claim

## What it is

A held claim is a claim whose lifetime extends past its acquirer's terminal to cover the holding subgraph: the acquirer plus every directly-declared inheritor. Marked by `rimsky_claim_handle.is_held = TRUE`. Per-member state tracked in `rimsky_claim_holders` rows keyed by `(claim_handle_id, holder_node)` with `state ∈ {active, completed, failed}`.

## Purpose

Some claims must remain held across a multi-node subgraph: a parent node opens a transaction, child nodes write to it, the transaction commits only after every child terminates successfully. Held claims express that dependency outside the per-node dispatch lifetime.

## Boundaries

Owns: the holding-subgraph state ledger, the aggregate-outcome rule, the `ON DELETE SET NULL` shape on `worker_request_id` that lets the handle outlive the parent. Does NOT own: claim disposition verb dispatch (see `auto-terminal`), conflict checking (see `claim-handle`). Adjacent: `claim-handle`, `auto-terminal`, `worker-request`, `parked-state`.

## Invariants

- Aggregate outcome is strict: all-completed → `Commit`; any-failed → `Abandon` (`@blessed-invariant 13`).
- Auto-terminal fires exactly once per held claim, race-safe via `SELECT … FOR UPDATE` on the row.
- Held handles persist across the `worker_request` parent's deletion (`ON DELETE SET NULL`, not `CASCADE`).
- `rimsky_claim_holders.state` CHECK forbids non-{active,completed,failed} values; once a holder is `failed`, the aggregate is `failed` (no `discard_then_retry` recovery in scope).

## Aliases and historical names

The legacy `lock_holder_id` FK column on the holders table is renamed to `claim_handle_id` post-phase-5. Some sketches use "holding subgraph" as the colloquial name; "held claim" is the schema/Go vocabulary.

## Open within this concept

- Legacy FK column name `lock_holder_id` on the holders table (renamed to `claim_handle_id` post-Phase-5) still surfaces in older prose — see `tensions/lock-holder-vs-claim-handle-legacy.md`.

