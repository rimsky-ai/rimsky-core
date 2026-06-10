---
concept: orphan-reaper
status: as-is
aliases: []
---

# Orphan reaper

## What it is

A periodic sweep that hard-deletes stale rows from the node-run ledger and the claim-handle ledger. The runtime carries a family of sweep functions — stale-heartbeat, orphaned-node-run, ready, and orphaned-claim-handle sweeps. Cutoff: `5 × heartbeat_interval`. A claimant-guarded delete predicate ensures live owners are never clobbered.

## Purpose

When a supervisor crashes mid-run, its heartbeat stops; somebody has to clean up the rimsky-side rows so the same scope/dispatch becomes claimable again. The reaper does the rimsky-side delete; the producer's own TTL handles producer-side cleanup.

## Boundaries

Owns: the periodic sweep, the cutoff, the claimant-guarded delete. Does NOT own: producer-side state cleanup (producer's TTL), the bail path's explicit `Abandon` call (that's the orphaned-claim bail handler). Adjacent: `claim-handle`, `node-run`, `supervisor`, `parked-state` (rows skipped), `auto-terminal` (held handles).

## Invariants

- The reaper does NOT call the producer's `Abandon`. The orphaned-claim bail handler IS the deliberate exception that does.
- Sweep cutoff is `5 × heartbeat_interval` (`@blessed-invariant 6`). Same cutoff for both row types.
- All active-row deletes are claimant-guarded (`@blessed-invariant 4`).
- The claim-handle reaper skips non-`active` rows (its predicate matches only active rows past the expiry cutoff); the held-durable preservation property follows from the state-column structure. Terminal rows are owned by the claim-handle retention sweep (subgraph at cutoff) or by the asset Release path (durable, never reaped).
- `phase='parked'` rows are explicitly skipped (parked nodes don't heartbeat).
