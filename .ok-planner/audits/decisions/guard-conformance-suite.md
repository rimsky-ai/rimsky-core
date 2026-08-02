---
audit: guard-conformance-suite
artifact: decision:guard-conformance-suite
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:26Z
---

# The driver-parity ClaimantGuard suite proves wrong-claimant is a no-op on every guarded mutation, on both drivers

Supported. `lib/foundation/persistence/conformance/conformance.go::Suite` (its `t.Run("ClaimantGuard", ...)` block carries the decision's own citation tag) is invoked once from `conformance_test.go` for both `TestConformancePostgres` and `TestConformanceSQLite`, giving identical assertions against both drivers. Enumerating from the interface definitions: of `ClaimHandleTable`'s 18 methods (`lib/foundation/persistence/claim_handles.go`), the 13 that take an explicit supervisor/claimant argument (`UpdateAddress`, `UpdatePayload`, `UpdateRealizedWriteSemantics`, `UpdateClaimScope`, `UpdateNodeRunID`, `ReassignHolderSupervisor`, `Promote`, `SetVersionID`, `Delete`, `DeleteIfExpired`, `SetAggregationPolicy`, `BumpExpectedChildrenCount`, `BumpChildOutcomeCount`) are each exercised for a wrong-claimant no-op in `claimant_guard.go`; of `Queue`'s claimant-taking mutating methods (`lib/foundation/persistence/node_runs.go`), `ClaimDispatchRow`, `PromoteClaimedToRunning`, `Complete`, `ForceComplete`, `RemoveForNode`, `ForceRemoveForNode`, `ReleaseClaim`, `ForceReleaseClaim`, and `ParkActive` are covered inside the ClaimantGuard block, and `ReleaseClaimWithDisposition` is covered by an equivalent wrong-claimant-is-no-op assertion in the same driver-parity `Suite` (`recovery_aware_dispatch.go::testRecoveryDispositionStamps`). `RegisterAsyncAck` and `BumpLastProgressAt` were checked and found not claimant-guarded (no `WHERE claimant = ...` predicate in either driver's implementation), so they are correctly out of this population.
