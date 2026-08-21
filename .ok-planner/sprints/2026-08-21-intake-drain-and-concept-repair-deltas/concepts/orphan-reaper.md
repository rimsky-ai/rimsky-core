---
concept: orphan-reaper
---

# Orphan reaper

## What it is

The orphan reaper is a family of sweeps that reclaim orphaned work. Rimsky keeps the sweeps separate rather than folding them into one reaper. Each sweep is named for what it reclaims; an orphaned run returns to the queue, an expired handle is removed, and a dropped dispatch channel is cleaned up in band. Each sweep reads its own signal to decide that work is orphaned: a supervisor's outgoing dispatch channel dropping, a run falling quiet past a deadline, or a liveness lease lapsing.

## Purpose

The orphan reaper makes an orphaned scope or dispatch claimable again. A holder can vanish while rimsky's own rows still record its work as held, and every later attempt on that scope or dispatch then finds it taken. The reaper clears those rows.

## Boundaries

The orphan reaper owns its periodic sweeps, the cutoff each sweep uses, and the claimant-guarded release and delete. It does not own producer-side cleanup, which each producer performs on its own expiry (see `claim-producer`). It does not own the explicit abandon a bail path performs on an orphaned claim. See also: `claim-handle`, `node-run`, `frame`, `supervisor`, `parked-state`, `auto-terminal`.
