---
concept: orphan-reaper
status: as-is
aliases: []
---

# Orphan reaper

## What it is

A periodic sweep that hard-deletes stale rows from the node-run ledger and the claim-handle ledger. The runtime carries a family of sweep functions — stale-recovery, orphaned-node-run, ready, and orphaned-claim-handle sweeps. Cutoff: the per-dispatch `max_runtime` deadline for orphaned-row detection (absolute upper bound), with the supervisor's outgoing gRPC client failure driving in-band cleanup for sync dispatches and the persistent `last_progress_at` quiet-period check (`now - last_progress_at > max_quiet_period`, when set) driving cleanup for async dispatches. A claimant-guarded delete predicate ensures live owners are never clobbered.

## Purpose

When a supervisor's outgoing dispatch RPC drops or an async executor goes silent past its quiet-period, somebody has to clean up the rimsky-side rows so the same scope / dispatch becomes claimable again. The reaper does the rimsky-side delete; the producer's own TTL handles producer-side cleanup.

## Boundaries

Owns: the periodic sweep, the cutoff, the claimant-guarded delete. Does NOT own: producer-side state cleanup (producer's TTL), the bail path's explicit `Abandon` call (that's the orphaned-claim bail handler). Adjacent: `claim-handle`, `node-run`, `supervisor`, `parked-state` (rows skipped), `auto-terminal` (held handles).

## Invariants

- The reaper does NOT call the producer's `Abandon`. The orphaned-claim bail handler IS the deliberate exception that does.
- Sweep cutoff for active rows is `max_runtime` (the per-dispatch absolute-deadline, when set); the supervisor's gRPC client failure drives in-band cleanup for sync RPCs without waiting for the sweep. For async dispatches, the quiet-period check (`now - last_progress_at > max_quiet_period`, when set) is an early-trigger before `max_runtime`. Claim-handle and node-run sweeps share the same cutoff.
- All active-row deletes are claimant-guarded (`@blessed-invariant 4`).
- The claim-handle reaper skips non-`active` rows (its predicate matches only active rows past the expiry cutoff); the held-durable preservation property follows from the state-column structure. Terminal rows are owned by the claim-handle retention sweep (subgraph at cutoff) or by the asset Release path (durable, never reaped).
- `phase='parked'` rows are explicitly skipped (parked is settled with respect to liveness; no quiet-period or RPC-state to observe).
