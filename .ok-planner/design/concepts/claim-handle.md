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

Post-2026-05-15 the row also carries:

- `parent_claim_handle_id UUID NULL` — FK self, pointing at the parent claim in a sub-claim chain. NULL for top-level claims; non-NULL for sub-claims spawned via `ClaimProducer.SplitScope`. Auto-terminal walks bottom-up over this FK.
- `lifetime TEXT NOT NULL` — `subgraph` (default) | `durable`. Selects auto-terminal behavior: `subgraph` deletes the row on holding-subgraph completion; `durable` flips `held_durable: true` and persists past completion.
- `held_durable BOOLEAN NOT NULL DEFAULT FALSE` — marks a row that survived auto-terminal Commit on a `lifetime: durable` claim. Released only by explicit operator action (`DELETE /instances/{id}/assets/{alias}`) or instance termination. The orphan-claim reaper skips `held_durable = true` rows.
- `version_id TEXT NULL` — the canonical version_id returned by `ClaimProducer.Commit` for DataProcessing-capable claims; surfaces in lineage records (`record_kind: claim_commit`) and asset version-history queries.
- `producer_candidate_handle BYTEA NULL` — opaque candidate_handle from `DataProcessing.BeginCandidate`; lives on sub-claim rows for fan-out-with-DataProcessing flows. Threaded through to the leaf executor's `ExecuteRequest.candidate_handle`.

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

A **held** claim is a claim whose lifetime extends past its acquirer's terminal to cover the holding subgraph: the acquirer plus every directly-declared inheritor. Marked by `col:rimsky_claim_handles.is_held = TRUE`. Per-member state tracked in `table:rimsky_claim_holders` rows keyed by `(claim_handle_id, holder_run_id)` with `state ∈ {active, completed, failed}`.

Post-2026-05-15 the holder key is `holder_run_id` (FK to `rimsky_node_runs.id`), not the legacy `holder_node_id`. This reflects the run-tree extension: holders are runs, not nodes. The acquirer's own holder row is inserted at acquire-time (`runner_acquire.go::insertHeldClaimHoldersAtAcquire`); co-holder rows (declared via `holds:`) are inserted at the co-holder's own acquire-time (`runner_acquire.go::insertCoHolderClaimHoldersAtAcquire`).

Held-variant invariants:

- Aggregate outcome is strict: all-completed → `Commit`; any-failed → `Abandon` (`@blessed-invariant 13`).
- Auto-terminal fires exactly once per held claim, race-safe via `SELECT … FOR UPDATE` on the row.
- Held handles persist across the `rimsky_node_runs` parent's deletion (`ON DELETE SET NULL`, not `CASCADE`).
- `col:rimsky_claim_holders.state` CHECK forbids non-{active,completed,failed} values; once a holder is `failed`, the aggregate is `failed` (no `discard_then_retry` recovery in scope).
- **Held-durable claim handles persist across instance dispatches** (post-2026-05-15 `@blessed-invariant 22`). A claim handle with `held_durable = true` is not deleted by holding-subgraph auto-terminal; only by explicit operator action or instance termination. The orphan-claim reaper skips `held_durable = true` rows.

### Authoring: held vs unheld

A template declares inheritors on each node's `inherits:` clause; the claim opened by a node becomes "held" implicitly when one or more downstream nodes declare it as an inherited claim. The author does not flip a flag — the holding-subgraph membership is derived from the template's edges. Auto-terminal fires for the claim when every node in the holding subgraph (acquirer plus inheritors) has reached a non-active state.

## Aliases and historical names

The legacy table name was `rimsky_lock_holders`; Phase-5 consolidation renamed it to `rimsky_claim_handle` (singular), and the 2026-05-12 nomenclature resolution pluralized it to `rimsky_claim_handles`. The legacy Go-side struct field `lock_holder_id` is renamed to `claim_handle_id` on the child `rimsky_claim_holders` table.

## Open within this concept

- "5 × heartbeat" cutoff is asymmetric across the two row types (node-run uses `last_heartbeat_at + interval`; claim_handle uses computed `expires_at`) — see `tensions/heartbeat-cutoff-asymmetry.md`.
- Orphan reaper does NOT call `Abandon`; bail path DOES — annotated asymmetry, easy to miss. See `tensions/reaper-vs-bail-abandon-asymmetry.md`.

## Notes

- Held-variant content folded in from former `concept:held-claim` per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #16).
