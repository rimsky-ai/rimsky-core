---
topic: orphan-reaper-no-producer-abandon
kind: discipline
---

# Periodic orphan reaper hard-deletes claim handles WITHOUT firing producer `Abandon`

## Description

When a supervisor crashes mid-claim, the producer may have created internal state — a staging directory, an items-table row flip, an MVCC transaction — that needs cleanup. The orphan reaper could either fire `ClaimProducer.Abandon` on every expired claim-handle row (mirroring the bail path used by `handleOrphanedClaim`) or just delete the rimsky-side row and let the producer's own TTL clean up.

Rimsky's reaper takes the second path. `SweepClaimHandles` (`foundation/integration/orphan_reaper.go:17-25`) hard-deletes `rimsky_claim_handle` rows where `expires_at < now()` claimant-guarded. It does NOT call `ClaimProducer.Abandon`. The producer's own TTL/sweep handles cleanup of its internal state — per foundation contract §4.5. Companion sweeps cover the worker-request side (`SweepStaleHeartbeats`, `SweepOrphanedClaims`, `SweepReady`, `SweepLockHolders` — all in `foundation/integration/`).

The bail path in `handleOrphanedClaim` (`foundation/integration/runner_acquire.go:776-810`) is the deliberate exception: it DOES call `Abandon` because the supervisor knows what it just opened — it just lost the verify-before-run race against another supervisor. Annotated explicitly at `runner_acquire.go:781-784`: "The two paths are deliberately distinct: the bail path fires Abandon because the supervisor knows what it just did; the reaper does NOT fire Abandon because it can't distinguish a crashed-supervisor state from any other."

The 5×heartbeat_interval cutoff (`@blessed-invariant 6` at `foundation/integration/conductor.go:30-50`) governs when a row becomes orphan. A crashed-supervisor's claim-handle row is gone within `5 × heartbeat_interval` regardless of producer state.

A reaper that fired `Abandon` on every expired row would call producer verbs in cases where the producer never had any state to abandon (e.g. an `Open` call that returned `Unavailable` just before the row expired). Idempotency in `claim_id` would handle that semantically, but the call still costs a round trip and obscures the operational signal "this supervisor crashed and we don't know what it had open." Producers expose their own TTL because they're the only ones who can decide whether their internal state needs explicit cleanup or is already self-cleaning (filesystem stagings are explicit; MVCC transactions auto-rollback on idle).

The third path (no reaper at all) is rejected because `rimsky_claim_handle` rows would accumulate forever and block subsequent claims with the same scope from getting a clean conflict-check view — the conflict predicate would see "yes, this scope is held" forever.

`docs/concepts/claim-handle.md` reflects this: "Stale claim handles (where the supervisor that acquired them has died) are reaped by the orphan reaper; the reaper's `5 × heartbeat_interval` cutoff is the time after which a non-heartbeating supervisor's claims are presumed orphaned."

## Code surface

- `foundation/integration/orphan_reaper.go` — entire file (~150 lines).
- `foundation/integration/runner_acquire.go:776-810` — `handleOrphanedClaim` (the bail path that DOES fire `Abandon`).
- `foundation/integration/conductor.go:30-50` — invariant 6 cutoff.
- `foundation/integration/sweep_parked.go` — separate sweep for `phase='parked'` (which orphan reaper skips).
- `foundation/integration/orphan_blobs.go` — separate orphan sweep for blob handles.

## Prose surface

- `CLAUDE.md` "Non-obvious gotchas" — "Parked nodes do not heartbeat." (orphan reaper skips them).
- `docs/concepts/claim-handle.md` — reaper cutoff and stale claims.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` §4.5 — foundation contract for cleanup ownership.

## Adjacent topics

- `2026-05-10-claimant-guarded-release` — reaper uses the same predicate.
- `2026-05-10-verify-before-run-guard` — bail path's `Abandon` call.
- `2026-05-10-parked-state-and-resume` — parked rows explicitly skipped.
- `2026-05-10-worker-request-phase-lifecycle` — phase-aware sweeps.

## Observations

- The asymmetry between the bail path (fires `Abandon`) and the periodic reaper (doesn't) is annotated but easy to miss in a casual reading; both functions live in `foundation/integration/` and look similar. The annotation block at `runner_acquire.go:781-784` is the only inline explanation.
- The "producer's own TTL handles its data" contract is a hard requirement on producer implementers — a producer that doesn't implement a TTL/sweep accumulates orphan state. The conformance binary (`cmd/rimsky-claim-producer-conformance`) checks this expectation; absence in a third-party producer is silent.
- The 5×heartbeat_interval cutoff lives at `conductor.go` (not `orphan_reaper.go`), which is the timing-config home. Tuning the heartbeat interval changes the orphan cutoff proportionally.
- A held claim handle whose parent worker-request was deleted (cleanup leftovers from an abnormal flow) is reaped on `expires_at` lapse rather than via auto-terminal — the held-claim auto-terminal mechanism requires `rimsky_claim_holders` rows to be in `completed`/`failed`, but if the parent is gone the resolution path is also gone. The `expires_at` reap covers this corner.
