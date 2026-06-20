---
topic: verify-before-run-guard
kind: invariant
---

# Verify-before-run: re-read `claimed_by` after the acquisition tx commits; bail with `Abandon` if ownership moved

## Description

Postgres MVCC snapshot isolation means a transaction reading `rimsky_worker_request.claimed_by` inside the acquisition tx sees the row as the supervisor's own, even if another supervisor concurrently completed a claim against the same row in the gap between the candidate-selection read and the COMMIT. The conflict-check + claim transaction protects against most races (per the deterministic lock-ordering rule plus the per-scope advisory lock) but not against the rare cross-tx handoff.

The verify-before-run rule (annotated at `foundation/integration/runner.go:31-39`): after the acquisition tx commits, the runner does a separate single-statement read against `rimsky_worker_request.claimed_by`. The implementation:

- `foundation/integration/runner_acquire.go:756-765` — calls `Queue.GetClaimedBy`.
- `foundation/persistence/postgres/queue.go:309-330` — the read; annotated `verify-before-run.`

If ownership has moved, `handleOrphanedClaim` (`foundation/integration/runner_acquire.go:776-810`) bails:

1. Call `ClaimProducer.Abandon` to release the producer-side state the supervisor just opened.
2. Delete the claim-handle row claimant-guarded (`AND holder_supervisor_id = supervisor_id`).
3. Emit `orphaned_claim_lost_race` event.
4. Abort the dispatch; the next supervisor picks the row up via normal candidate selection.

The function comment at `foundation/integration/runner.go:31-39` is explicit:

> Running the check inside the tx would race with other supervisors that also see the row as theirs because of MVCC snapshot isolation; the bail here is what catches the rare cross-transaction handoff and keeps the ownership invariant intact.

The asymmetry between the bail path (which calls `Abandon`) and the periodic orphan reaper (which does NOT call `Abandon` per `2026-05-10-orphan-reaper-no-producer-abandon`) is intentional: the bail path knows exactly what it just opened on the producer side, so it can clean up explicitly; the periodic reaper can't distinguish a crashed-supervisor state from any other state, so it leaves producer cleanup to the producer's TTL.

The producer's `Abandon` verb is required to be idempotent in `claim_id` (per the foundation contract). The bail path may race against a separate code path on the producer side (the producer's own TTL might already be firing), and idempotency in `claim_id` is what makes the double-call safe.

Every successful dispatch incurs one extra small read after the acquisition tx commits. The `claimed_by = supervisor_id` predicate becomes the single point of truth for "still owned" — load-bearing for the claimant-guarded release invariants (`2026-05-10-claimant-guarded-release`).

CLAUDE.md "Blessed invariants" §5 captures this exactly: "Verify-before-run. Supervisor re-reads `claimed_by` immediately before calling the executor; bails as `orphaned_claim_lost_race` if ownership moved."

## Code surface

- `foundation/integration/runner.go:31-39` — verify-before-run annotation.
- `foundation/integration/runner_acquire.go:756-810` — verify-before-run read + `handleOrphanedClaim` bail.
- `foundation/persistence/postgres/queue.go:309-330` — `GetClaimedBy` annotated.
- `foundation/persistence/sqlite/queue.go:401-420` — SQLite mirror.
- `test/scenarios/verify_before_run_race_test.go` — regression test.

## Prose surface

- `CLAUDE.md` "Blessed invariants" §5.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — verify-before-run in the contract.

## Adjacent topics

- `2026-05-10-claimant-guarded-release` — `claimed_by = supervisor_id` is the load-bearing predicate.
- `2026-05-10-orphan-reaper-no-producer-abandon` — the asymmetry between bail and reaper.
- `2026-05-10-atomic-acquisition-decoupled-tx` — what the verify-before-run guards against.
- `2026-05-10-state-machine-no-self-loop` — sibling load-bearing guard for state transitions.

## Observations

- The verify-before-run is a separate query, not part of the acquisition tx's view. This makes it visible to ROW-level concurrent UPDATEs that may have happened in the millisecond gap between COMMIT and the read. The read is `READ COMMITTED` (not snapshot-isolated) so it sees the latest state.
- The `orphaned_claim_lost_race` event is the operational signal that the race fired. Under healthy load it should be rare; a sudden uptick indicates either a misbehaving claim-store (slow `Open` widening the contention window) or a supervisor-pool sizing issue.
- The bail path's call to `Abandon` happens before the claim-handle row delete; the order matters because if `Abandon` fails, the row stays so the orphan reaper picks it up later. A delete-first ordering would leave a producer-side leak.
- The scenario test (`test/scenarios/verify_before_run_race_test.go`) drives the race deliberately and is part of the regression backstop. CLAUDE.md mentions it as the canonical reference for the invariant.
