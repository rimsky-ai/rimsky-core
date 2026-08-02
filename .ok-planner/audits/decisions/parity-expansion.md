---
audit: parity-expansion
artifact: decision:parity-expansion
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:46Z
---

# The driver-parity suite runs one broad behavior library against both drivers

Supported. `lib/foundation/persistence/conformance/conformance.go`'s `Suite` function is a single ~140-subtest library spanning queue/dispatch behavior (`DispatchClaimRelease`, `QueueInTxAndDispatchNode`, `RecoveryAwareDispatch`, the `SelectCandidates*` family), claim-handle behavior (`ClaimHandleQueries`, the 17-case `ClaimantGuard` group, `ClaimHandlesUpdateClaimScope`, claim-scope conflict tests), and frame behavior (`FrameLifecycle`, `FrameSettlement`, `RunScopeLifecycle`, park/resume). `conformance_test.go` calls this exact same `Suite` from both `TestConformancePostgres` (against a real `pgtest`-backed Postgres database) and `TestConformanceSQLite` (against a real migrated SQLite database) — the wrong-claimant guard suite `decision:guard-conformance-suite` describes is the `ClaimantGuard` subtree within it, confirming it as one slice of the whole rather than a separate suite.
