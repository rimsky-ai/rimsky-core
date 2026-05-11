---
tension: reaper-vs-bail-abandon-asymmetry
category: muddy-boundary
status: open
affects:
  - orphan-reaper
  - claim-producer
  - supervisor
---

# Periodic orphan reaper does NOT call `Abandon`; bail path `handleOrphanedClaim` does — annotated asymmetry, easy to miss

## What is muddy

Two release paths exist for claim handles, sitting in adjacent files in `foundation/integration/`:

- **Periodic orphan reaper** (`orphan_reaper.go::SweepClaimHandles`) — hard-deletes `rimsky_claim_handle` rows whose `expires_at < now()`. **Does NOT call `ClaimProducer.Abandon`**. The producer's own TTL handles producer-side cleanup.
- **Verify-before-run bail path** (`runner_acquire.go::handleOrphanedClaim`) — fires when verify-before-run sees ownership moved. **DOES call `Abandon`** because "the supervisor knows what it just did."

The reasoning is annotated at `runner_acquire.go:781-784` but both functions look similar and live close together. A new contributor adding a "claim cleanup" admin operation has to know which path to mirror.

## Why it matters

A reaper that started firing `Abandon` would silently double-call the producer (cost + log noise); a bail path that stopped firing it would leak producer-side state. The asymmetry is correct but fragile.

## Resolution candidates (do NOT pick)

- Surface the asymmetry in a single "release-paths" prose section in `docs/concepts/claim-handle.md`.
- Encode the asymmetry in the function names (`SweepClaimHandlesNoAbandon`, `BailOrphanedClaimWithAbandon`).
- Add a regression test that fails if the reaper starts firing `Abandon`.

## Evidence

- `_discover/2026-05-10-orphan-reaper-no-producer-abandon.md` Observations bullet 1.
- `_discover/2026-05-10-verify-before-run-guard.md` "asymmetry" para.
- `foundation/integration/runner_acquire.go:781-784` annotation.

