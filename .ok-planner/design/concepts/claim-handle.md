---
concept: claim-handle
status: as-is
aliases:
  - lock-holder (legacy)
  - held-claim (folded; see Held variant below)
references:
  - _discover/2026-05-10-worker-request-phase-lifecycle.md
  - _discover/2026-05-10-claimant-guarded-release.md
  - _discover/2026-05-10-lock-state-in-rimsky-not-producer.md
  - _discover/2026-05-10-auto-terminal-aggregate-resolution.md
  - _discover/2026-05-10-orphan-reaper-no-producer-abandon.md
---

# Claim handle

## What it is

`claim` is the protocol-layer noun returned by `ClaimProducer.Open`; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing. They have different invariants by layer — `@blessed-invariant 20` (claim content inert) gates content; `@blessed-invariant 4` (claimant-guarded release) gates the persistence row.

`rimsky_claim_handles` is the rimsky-side ledger row representing one acquired claim (or named-lock acquisition). Columns: `lock_kind ∈ {named, scope}`, `lock_name`, `scope_data`, `holder_supervisor_id`, `expires_at`, `is_held`, `realized_write_semantics`, optional `node_run_id` (FK with `ON DELETE SET NULL`). Replaces the legacy `rimsky_lock_holders` table.

## Purpose

The single source of truth for "who holds what right now." Conflict-check predicates walk this table only; orphan reaping operates on this table; held-claim resolution deletes from this table. Decouples rimsky-side bookkeeping from producer-side state.

## Boundaries

Owns: the lock-state ledger, claimant-guarded mutation predicates, the `is_held`+`node_run_id ON DELETE SET NULL` shape that lets held handles outlive their parent. Does NOT own: producer-internal state (see `concept:claim-producer`), heartbeats (those are on `concept:node-run`), claim-disposition verb dispatch (see `concept:auto-terminal`). Adjacent: `concept:claim`, `concept:node-run`, `concept:auto-terminal`, `concept:supervisor`, `concept:orphan-reaper`, `concept:inertness`.

## Invariants

- Every `DELETE FROM rimsky_claim_handles` and every heartbeat-refresh `UPDATE` carries `AND holder_supervisor_id = supervisor_id` (`@blessed-invariant 4`).
- `holder_supervisor_id` is NOT NULL on the live row.
- `node_run_id` FK uses `ON DELETE SET NULL` (not CASCADE) so held handles survive their parent's deletion until auto-terminal explicitly resolves them.
- Lock state lives only in this table; producers do not persist or shadow it (`@blessed-invariant 9a`).
- The orphan reaper sweeps `expires_at < now()` rows but does NOT call `ClaimProducer.Abandon`; the bail path in `handleOrphanedClaim` is the deliberate exception that DOES fire `Abandon`.

### Held variant

A **held** claim is a claim whose lifetime extends past its acquirer's terminal to cover the holding subgraph: the acquirer plus every directly-declared inheritor. Marked by `col:rimsky_claim_handles.is_held = TRUE`. Per-member state tracked in `table:rimsky_claim_holders` rows keyed by `(claim_handle_id, holder_node_id)` with `state ∈ {active, completed, failed}`.

Held-variant invariants:

- Aggregate outcome is strict: all-completed → `Commit`; any-failed → `Abandon` (`@blessed-invariant 13`).
- Auto-terminal fires exactly once per held claim, race-safe via `SELECT … FOR UPDATE` on the row.
- Held handles persist across the `rimsky_node_runs` parent's deletion (`ON DELETE SET NULL`, not `CASCADE`).
- `col:rimsky_claim_holders.state` CHECK forbids non-{active,completed,failed} values; once a holder is `failed`, the aggregate is `failed` (no `discard_then_retry` recovery in scope).

### Authoring: held vs unheld

A template declares inheritors on each node's `inherits:` clause; the claim opened by a node becomes "held" implicitly when one or more downstream nodes declare it as an inherited claim. The author does not flip a flag — the holding-subgraph membership is derived from the template's edges. Auto-terminal fires for the claim when every node in the holding subgraph (acquirer plus inheritors) has reached a non-active state.

## Aliases and historical names

The legacy table name was `rimsky_lock_holders`; Phase-5 consolidation renamed it to `rimsky_claim_handle` (singular), and the 2026-05-12 nomenclature resolution pluralized it to `rimsky_claim_handles`. The legacy Go-side struct field `lock_holder_id` is renamed to `claim_handle_id` on the child `rimsky_claim_holders` table.

## Open within this concept

- "5 × heartbeat" cutoff is asymmetric across the two row types (node-run uses `last_heartbeat_at + interval`; claim_handle uses computed `expires_at`) — see `tensions/heartbeat-cutoff-asymmetry.md`.
- Orphan reaper does NOT call `Abandon`; bail path DOES — annotated asymmetry, easy to miss. See `tensions/reaper-vs-bail-abandon-asymmetry.md`.

## Notes

- Held-variant content folded in from former `concept:held-claim` per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #16).
