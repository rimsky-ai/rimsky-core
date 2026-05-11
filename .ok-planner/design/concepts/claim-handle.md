---
concept: claim-handle
status: as-is
aliases:
  - lock-holder (legacy)
references:
  - _discover/2026-05-10-worker-request-phase-lifecycle.md
  - _discover/2026-05-10-claimant-guarded-release.md
  - _discover/2026-05-10-lock-state-in-rimsky-not-producer.md
  - _discover/2026-05-10-auto-terminal-aggregate-resolution.md
  - _discover/2026-05-10-orphan-reaper-no-producer-abandon.md
---

# Claim handle

## What it is

`rimsky_claim_handle` is the rimsky-side ledger row representing one acquired claim (or named-lock acquisition). Columns: `lock_kind ∈ {named, scope}`, `lock_name`, `scope_data`, `holder_supervisor_id`, `expires_at`, `is_held`, `realized_write_semantics`, optional `worker_request_id` (FK with `ON DELETE SET NULL`). Replaces the legacy `rimsky_lock_holders` table.

## Purpose

The single source of truth for "who holds what right now." Conflict-check predicates walk this table only; orphan reaping operates on this table; held-claim resolution deletes from this table. Decouples rimsky-side bookkeeping from producer-side state.

## Boundaries

Owns: the lock-state ledger, claimant-guarded mutation predicates, the `is_held`+`worker_request_id ON DELETE SET NULL` shape that lets held handles outlive their parent. Does NOT own: producer-internal state (see `claim-producer`), per-holder-node subgraph state (see `held-claim`), heartbeats (those are on `worker-request`). Adjacent: `claim`, `worker-request`, `held-claim`, `auto-terminal`, `supervisor`, `orphan-reaper`.

## Invariants

- Every `DELETE FROM rimsky_claim_handle` and every heartbeat-refresh `UPDATE` carries `AND holder_supervisor_id = supervisor_id` (`@blessed-invariant 4`).
- `holder_supervisor_id` is NOT NULL on the live row.
- `worker_request_id` FK uses `ON DELETE SET NULL` (not CASCADE) so held handles survive their parent's deletion until auto-terminal explicitly resolves them.
- Lock state lives only in this table; producers do not persist or shadow it (`@blessed-invariant 9a`).
- The orphan reaper sweeps `expires_at < now()` rows but does NOT call `ClaimProducer.Abandon`; the bail path in `handleOrphanedClaim` is the deliberate exception that DOES fire `Abandon`.

## Aliases and historical names

The legacy table name was `rimsky_lock_holders`; phase-5 consolidation renamed it. The legacy Go-side struct field `lock_holder_id` is renamed to `claim_handle_id` on the child `rimsky_claim_holders` table.

## Open within this concept

- "5 × heartbeat" cutoff is asymmetric across the two row types (worker_request uses `last_heartbeat_at + interval`; claim_handle uses computed `expires_at`) — see `tensions/heartbeat-cutoff-asymmetry.md`.
- Orphan reaper does NOT call `Abandon`; bail path DOES — annotated asymmetry, easy to miss. See `tensions/reaper-vs-bail-abandon-asymmetry.md`.
- Legacy name `rimsky_lock_holders` / `lock_holder_id` still surfaces in older sketches and prose despite the Phase-5 rename — see `tensions/lock-holder-vs-claim-handle-legacy.md`.

