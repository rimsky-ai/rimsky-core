---
audit: fold-ownership-bail
artifact: decision:fold-ownership-bail
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# The verify-before-run bail path routes through the unified claim-handle resolution engine

Supported. `lib/runtime/runner_acquire_postcommit.go::bailAcquiredLock` calls `ResolveClaimHandleTerminal` (the same engine `runner_terminal_release.go`'s ordinary terminal path uses) with `Source: OwnershipBail`, running the identical sequence — data-processing terminal dispatch, producer-verb outbox enqueue, forensics, descendant-claim cancellation, deferred held cascade, fan-out settlement — before the bail-specific final step (claimant-guarded `Delete` instead of `promoteHandleState`). `test/scenarios/verify_before_run_bail_engine_route_test.go` proves this end-to-end: exactly one Abandon fires (targeting the claim the matching Open minted), no Commit, the bailed row is deleted while a different supervisor's decoy row survives (claimant-guarded), no `terminal/*` or `claim_resolution.*` signal is emitted (an admin path, not a run-signal path), and exactly one `orphaned_claim_lost_race` forensic event is recorded. Checked the other `ClaimHandles.Delete` call sites in `lib/runtime` (`child_execution.go`, `instance_kill.go`, `runner_terminal_release.go`) and confirmed each is the documented, distinct named-lock-release path (no producer verb involved), not a second ownership-bail implementation; the one nil-producer branch inside `bailAcquiredLock` itself is a direct delete only for producer-less named locks, which the engine's own precondition (`Producer != nil`) refuses to accept, so it is not a competing duplicate of the engine's producer-verb sequence.
