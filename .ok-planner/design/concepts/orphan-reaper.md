---
concept: orphan-reaper
status: as-is
aliases: []
references:
  - _discover/2026-05-10-orphan-reaper-no-producer-abandon.md
  - _discover/orphan-claim-cutoff-five-heartbeats.md
  - _discover/2026-05-10-claimant-guarded-release.md
  - _discover/2026-05-10-verify-before-run-guard.md
---

# Orphan reaper

## What it is

A periodic sweep that hard-deletes stale rows from `table:rimsky_node_runs` and `table:rimsky_claim_handles`. Sweep functions in `runtime/`: `SweepStaleHeartbeats`, `SweepOrphanedNodeRuns` (formerly `SweepOrphanedClaims`), `SweepReady`, plus `orphan_reaper.go::SweepOrphanedClaimHandles` (formerly `SweepClaimHandles`). Cutoff: `5 × heartbeat_interval`. Claimant-guarded `DELETE` predicate so live owners are never clobbered.

## Purpose

When a supervisor crashes mid-run, its heartbeat stops; somebody has to clean up the rimsky-side rows so the same scope/dispatch becomes claimable again. The reaper does the rimsky-side delete; the producer's own TTL handles producer-side cleanup.

## Boundaries

Owns: the periodic sweep, the cutoff, the claimant-guarded delete. Does NOT own: producer-side state cleanup (producer's TTL), the bail path's explicit `Abandon` call (that's `handleOrphanedClaim`). Adjacent: `claim-handle`, `node-run`, `supervisor`, `parked-state` (rows skipped), `auto-terminal` (held handles).

## Invariants

- The reaper does NOT call `ClaimProducer.Abandon`. The bail path in `handleOrphanedClaim` IS the deliberate exception that does.
- Sweep cutoff is `5 × heartbeat_interval` (`@blessed-invariant 6`). Same cutoff for both row types.
- All active-row DELETEs are claimant-guarded (`@blessed-invariant 4`).
- The claim-handle reaper skips non-`active` rows (the predicate is `WHERE state = 'active' AND expires_at < now()`); the held-durable preservation property now flows from the state-column structure rather than a bool check. Terminal rows are owned by `SweepClaimHandleRetention` (subgraph at cutoff) or by the asset Release path (durable, never reaped).
- `phase='parked'` rows are explicitly skipped (parked nodes don't heartbeat).

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution` the sweep functions were named `SweepOrphanedClaims` (now `SweepOrphanedNodeRuns`) and `SweepClaimHandles` (now `SweepOrphanedClaimHandles`). The shared cutoff constant `OrphanedClaimTimeout` keeps its name; both reapers consult it.

## Open within this concept

- Heartbeat cutoff representation differs between node-run (`last_heartbeat_at + interval`) and claim-handle (computed `expires_at`) — see `tensions/heartbeat-cutoff-asymmetry.md`.
- "Reaper doesn't Abandon" vs "bail path does Abandon" annotated asymmetry, easy to miss — see `tensions/reaper-vs-bail-abandon-asymmetry.md`.

## Notes

- Sweep-function renames per `spec:2026-05-12-nomenclature-resolution` Group D.4 / D.5 (`SweepOrphanedClaims` → `SweepOrphanedNodeRuns`; `SweepClaimHandles` → `SweepOrphanedClaimHandles`).
- State-column refactor per `spec:2026-05-17-post-data-platform-cleanup`: the claim-handle reaper's skip rule was `held_durable = TRUE`; it's now `state != 'active'`. Functionally identical on the post-Stage-1 row set (held-durable rows had `state = 'committed'` after the backfill); the post-refactor predicate is broader (also skips committed-subgraph and abandoned rows, which are owned by the retention sweep). Sibling sweep `SweepClaimHandleRetention` (new) handles terminal-row cleanup at the configured trailing window.

