---
topic: orphan-claim-cutoff-five-heartbeats
kind: invariant
---

# Orphan-claim cutoff is `5 × heartbeat_interval`; same cutoff applies to `rimsky_claim_handle` and `rimsky_worker_request`

## Description

A supervisor heartbeats via `rimsky_worker_request.last_heartbeat_at` (refreshed during active dispatch) and the corresponding claim-handle row. A crashed supervisor stops heartbeating. The orphan reaper sweeps rows whose heartbeat is older than the cutoff and the supervisor that owned them is presumed dead.

`@blessed-invariant 6` (annotated at `foundation/integration/conductor.go:30-50` and `foundation/integration/orphan_reaper.go`): the orphan-claim cutoff is `5 × heartbeat_interval`. The same cutoff applies to both row types:

- `rimsky_worker_request` rows with `phase='active'` and `last_heartbeat_at < now() - (5 × heartbeat_interval)`.
- `rimsky_claim_handle` rows with `expires_at < now()` where `expires_at = last_heartbeat_at + (5 × heartbeat_interval)`.

The `5 ×` multiplier is the timing argument: a single missed heartbeat could be a transient hiccup (GC pause, network blip, brief scheduler stutter); five misses in a row is strong evidence of supervisor death. Pre-v1, this is a tunable but the multiplier is consistent across the two row types.

The cutoff scheduling lives in `foundation/integration/conductor.go`; the actual sweep is in `foundation/integration/orphan_reaper.go`. The sweep is claimant-guarded (`@blessed-invariant 4`): the DELETE includes `AND holder_supervisor_id = $1` so even if the cutoff timing is wrong (clock skew, scheduler precision), the live owner's identity prevents the clobber.

`modeling/scheduler/scheduler.go:151` has the corroborating annotation: "orphan-claim cutoff default = 5 × heartbeat_timeout."

CLAUDE.md "Blessed invariants" §6 puts it tersely: "Orphan-claim cutoff is `5 × heartbeat_interval`. Same cutoff applies to `rimsky_claim_handle` orphan reap."

The reaper does NOT call `ClaimProducer.Abandon` on swept rows (per `2026-05-10-orphan-reaper-no-producer-abandon`); the producer's own TTL/sweep handles producer-side cleanup. The bail path in `handleOrphanedClaim` (which knows what was just Opened) does call `Abandon` — that's the asymmetric pair.

Parked rows (`phase='parked'`) are explicitly skipped because parked nodes do not heartbeat (CLAUDE.md "Non-obvious gotchas"). A long-parked node could otherwise be reaped despite being legitimately paused.

The held-claim subgraph mechanism is orthogonal: held claim handles persist past their parent worker_request's active terminal (`ON DELETE SET NULL`) and are resolved via auto-terminal, not via the `expires_at` reap. The reap's expires_at lapse covers held claim handles whose parent was deleted (cleanup leftovers).

## Code surface

- `foundation/integration/conductor.go:30-50` — invariant 6 annotation + scheduling.
- `foundation/integration/orphan_reaper.go` — entire file; `SweepStaleHeartbeats`, `SweepOrphanedClaims`, `SweepClaimHandles`, `SweepReady`, `SweepLockHolders`.
- `foundation/persistence/postgres/queue.go:229-265` — `ReapStaleHeartbeats` SQL.
- `foundation/persistence/postgres/claim_handles.go` — claim-handle sweep SQL.
- `modeling/scheduler/scheduler.go:151` — annotation.
- `foundation/integration/sweep_parked.go` — separate sweep for `phase='parked'`.

## Prose surface

- `CLAUDE.md` "Blessed invariants" §6.
- `CLAUDE.md` "Non-obvious gotchas" — "Parked nodes do not heartbeat."
- `docs/concepts/claim-handle.md` — "5 × heartbeat_interval cutoff."

## Adjacent topics

- `2026-05-10-claimant-guarded-release` — DELETE predicate compounds with the cutoff.
- `2026-05-10-orphan-reaper-no-producer-abandon` — no producer Abandon on sweep.
- `2026-05-10-verify-before-run-guard` — re-read at the same column.
- `2026-05-10-parked-state-and-resume` — parked rows skipped.
- `2026-05-10-worker-request-phase-lifecycle` — `phase='active'` filter.

## Observations

- The `5 ×` multiplier is a configuration constant in code; CLAUDE.md "Non-obvious gotchas" makes it a blessed invariant rather than a knob. A future tuning that wanted (say) `3 ×` would have to argue for the new tolerance against the existing "transient hiccup vs death" calibration.
- The cutoff is symmetric across row types but the sweep is asymmetric: `rimsky_worker_request` uses `last_heartbeat_at < now() - 5*hb`; `rimsky_claim_handle` uses `expires_at < now()`. The latter is a computed column; the heartbeat-refresh path updates `expires_at` at the same time it updates `last_heartbeat_at`.
- Five sweeps (`SweepStaleHeartbeats`, `SweepOrphanedClaims`, `SweepClaimHandles`, `SweepReady`, `SweepLockHolders` per CLAUDE.md "What this repo is") all coordinate around the cutoff — separate functions for separate row types but the same timing.
- The orphan-reaper-runs-every-tick cadence is the scheduler-tick frequency; the actual `interval` between sweeps is determined by `conductor.go`. A tick that doesn't run (because the advisory tick lock is held by another scheduler replica) means the sweep doesn't fire that tick — but the next tick catches up.
