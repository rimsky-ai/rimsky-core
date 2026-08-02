---
audit: scratch-recovery
artifact: decision:scratch-recovery
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# Per-row scratch survives stale-recovery, retry, and recalculate

Supported. The cascade-driven recalculate enqueue path (`cascade_recalculate.go`) is the only production call site that populates `InitialScratchInline`/`InitialScratchHandle`/`InitialScratchHandleBackend` on row creation — checked all non-test, non-conformance-fixture call sites of those three fields across the module — loading them via `LoadScratch` from the prior run's row before inserting the new one. Dead-supervisor recovery (`conductor.go`'s `SweepExecutorDeadlines`) releases the orphaned claim on the same row via `ReleaseClaimWithDisposition`, never inserting a new row, so the row's already-persisted scratch needs no copy. In-place error-policy retry (`stampRetryAfterErrorInTx`) likewise stamps the disposition on the same row (`acq.NodeRunID` as both the row and its own "prior"). An end-to-end scenario suite (`scratch_round_trip_e2e_test.go`) exercises and asserts verbatim scratch survival across all three superseding dispositions — `retry_after_error`, `stale_recovery`, `recalculate`.
