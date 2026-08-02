---
audit: claimant-guard-helper
artifact: decision:claimant-guard-helper
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# Each driver routes claimant-guarded claim-handle mutations through one predicate helper

Supported. Postgres's `claimHandlesImpl` (`lib/foundation/persistence/postgres/claim_handles.go`) defines a single `claimantGuard(n)` helper producing `holder_supervisor_id = $n`, and SQLite's counterpart (`lib/foundation/persistence/sqlite/claim_handles.go`) defines a single `claimantGuardClause` constant; checked all claimant-guarded mutation methods in both files (`UpdateAddress`, `UpdatePayload`, `UpdateRealizedWriteSemantics`, `UpdateClaimScope`, `UpdateNodeRunID`, `ReassignHolderSupervisor`, `Promote`, `SetVersionID`, `Delete`, `DeleteIfExpired`, `SetAggregationPolicy`, `BumpExpectedChildrenCount`, `BumpChildOutcomeCount` — 13 methods per driver) and every one composes its `WHERE`/guard clause through the driver's single helper; no hand-written `holder_supervisor_id = ` guard predicate appears anywhere else in either file. `lib/foundation/persistence/conformance/conformance.go` runs a shared `ClaimantGuard` test group (17 sub-tests, including an explicit `UnguardedMutationCarveOuts` case) against both drivers via the conformance-suite factory pattern, and carries the decision's citation tag directly.
