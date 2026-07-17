---
concept: orphan-reaper
status: as-is
aliases: []
---

# Orphan reaper

## What it is

A family of purpose-named sweeps — deliberately never unified into a single reaper — that reclaim orphaned work. The node-run dispatch sweep and the in-frame dispatch sweep release orphaned claims back to the pending queue (the claimant is cleared and the row returns to reclaimable) rather than deleting rows; the claim-handle sweep hard-deletes expired claim-handle rows. Sync dispatches key on the supervisor's outgoing dispatch-channel connection state, driving in-band cleanup without waiting for a sweep. Async and in-frame dispatches key on the run's persistent progress timestamp checked against per-dispatch quiet-period and absolute-runtime deadlines. The claim-handle sweep keys separately, on a short renewed liveness lease (an expiry timestamp), unrelated to the dispatch deadlines. A claimant-guarded release/delete predicate ensures live owners are never clobbered.

## Purpose

When a supervisor's outgoing dispatch RPC drops, an async executor goes silent past its quiet-period, or a claim-handle's liveness lease lapses, somebody has to clean up the rimsky-side rows so the same scope / dispatch becomes claimable again. The reaper family does the rimsky-side cleanup; the producer's own TTL handles producer-side cleanup.

## Boundaries

Owns: the periodic sweeps, their cutoffs, the claimant-guarded release/delete. Does NOT own: producer-side state cleanup (producer's TTL), the bail path's explicit `Abandon` call (that's the orphaned-claim bail handler). Adjacent: `claim-handle`, `node-run`, `frame`, `supervisor`, `parked-state` (rows skipped), `auto-terminal` (held handles).

## Invariants

- The reaper does NOT call the producer's Abandon. The orphaned-claim bail handler IS the deliberate exception that does.
- Sweep cutoff differs by mechanism: the claim-handle sweep keys on a short renewed liveness lease (the expiry timestamp), not a dispatch deadline; the node-run and frame-dispatch sweeps key on per-dispatch quiet-period and absolute-runtime deadlines. The supervisor's dispatch-channel failure drives in-band cleanup for sync dispatches without waiting for the sweep; for async and in-frame dispatches, the quiet-period check (when configured) is an early trigger before the absolute-runtime deadline.
- All active-row releases and deletes are claimant-guarded (invariant 4).
- The claim-handle reaper skips non-active rows (its predicate matches only active rows past the expiry cutoff); the held-durable preservation property follows from the state-column structure. Terminal rows are owned by the claim-handle retention sweep, which reaps abandoned rows and committed-subgraph rows at cutoff; only committed-durable rows are exempt, surviving until the asset Release path fires.
- Rows in the parked state are skipped (parked is settled with respect to liveness; no quiet-period or dispatch-channel-state to observe).
