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
- The claim-handle reaper skips non-`active` rows (its predicate matches only active rows past the expiry cutoff); the held-durable preservation property now flows from the state-column structure rather than a bool check. Terminal rows are owned by the claim-handle retention sweep (subgraph at cutoff) or by the asset Release path (durable, never reaped).
- `phase='parked'` rows are explicitly skipped (parked nodes don't heartbeat).

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution` the orphaned-node-run sweep was named "orphaned claims" and the orphaned-claim-handle sweep was named "claim handles". The shared cutoff constant keeps its name; both reapers consult it.

## Open within this concept

- Heartbeat cutoff representation differs between node-run (a last-heartbeat timestamp plus interval) and claim-handle (a computed expiry timestamp) — see `tension:heartbeat-cutoff-asymmetry`.
- "Reaper doesn't Abandon" vs "bail path does Abandon" annotated asymmetry, easy to miss — see `tension:reaper-vs-bail-abandon-asymmetry`.

## Notes

- Sweep-function renames per `spec:2026-05-12-nomenclature-resolution` Group D.4 / D.5 (the orphaned-node-run and orphaned-claim-handle sweeps were renamed from their claims-era names).
- State-column refactor per `spec:2026-05-17-post-data-platform-cleanup`: the claim-handle reaper's skip rule was a held-durable boolean check; it's now a state-is-not-active check. Functionally identical on the post-Stage-1 row set (held-durable rows were committed after the backfill); the post-refactor predicate is broader (also skips committed-subgraph and abandoned rows, which are owned by the retention sweep). A new sibling retention sweep handles terminal-row cleanup at the configured trailing window.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

