---
issue: library-tests-off-standard
kind: human
category: enforcement-gap
artifacts:
  - decision:test-wallclock-lint-ratchet
  - decision:conformance-suite-per-protocol
status: open
opened: 2026-08-22T03:55:02Z
---

# Thirty-three library tests break the testing standard

## Problem

Thirty-three test functions under `lib/foundation`, `lib/protocols`, and `lib/services` break the testing standard at `.ok-plumbline/docs/testing.md`: ten reach their verdict from elapsed time or shared state, four prove nothing, and nineteen prove a behavior another test in the same package already proves. A reading audit over every test function in the three library modules found them.

The standard says a verdict never depends on elapsed time, and the project's rule adds that every admitted wait declares a class. `TestAcquireMigrationLock_HonorsContextCancel` in `lib/foundation/persistence/sqlite/advisory_locker_test.go` passes only when a bare 100-millisecond `context.WithTimeout` fires before the lock call returns, and declares no `//nolint:testwallclock-<class>`. The wall-clock lint did not catch it because the lint reads five constructs and `context.WithTimeout` is not one of them. The sibling test in the same file proves the same contention through the event-driven `awaited.Until`.

The standard says a test proves a behavior a user or a story owes. Three tests assert nothing that could fail on a behavior change:

- `TestRegistryConcurrentAddAndReadIsRaceFree` in `lib/foundation/locks/registry_test.go` and its twin of the same name in `lib/foundation/lifecycle/lifecycle_test.go` run goroutines and return after `wg.Wait()`; with `-race` banned from the suite, only a runtime map panic could fail them.
- `TestSQLiteUnifiedStackMaxOpenConnsAvoidsBeginStarvation` in `lib/foundation/persistence/sqlite/database_test.go` asserts a constant is at least 2 and opens no transaction.

The standard says to remove a test that duplicates a proof. Nine do:

- Four in `lib/foundation/cascade/state_test.go` (`TestFreshAndFailedAreTerminal`, `TestRunningToRunningUnderDispatchClaimedIsRejected`, `TestNextState_AggregateSettlementIsIllegalForLeafRuns`, `TestNextState_ReleaseReturnsAClaimedRunToStale`) each assert a cell of the exhaustive matrix `TestTransitionTable` already asserts.
- `TestModeCoexistsSymmetric` in `lib/foundation/locks/conflict_test.go` proves symmetry that `TestModeCoexistsMatrix` already proves by asserting both orderings.
- Four in `lib/foundation/persistence/blob_test.go` (`TestMemoryBackend`, `TestMemoryBackendReadRangeOutOfBounds`, `TestFilesystemBackend`, `TestFilesystemBackendReadRangeOutOfBounds`) repeat checks the shared blob conformance suite already runs against both backends through `TestBlobRoundtripBackends`.

The same three classes recur in `lib/protocols`.

Three tests break the determinism rule:

- `TestAwaitTerminal_ContextCancelledWhileAwaiting` in `lib/protocols/conformance/executor/await_terminal_test.go` rides the same unmarked 100-millisecond `context.WithTimeout` shape as the sqlite lock test above.
- `TestCheckSerialization9b_DetectsReaderLeaseSerialization` in `lib/protocols/conformance/claimproducer/serialization9b_test.go` inherits a 2-second deadline from the production check it calls, which uses `context.DeadlineExceeded` to tell a blocked claim from an available one.
- `TestRun_AllowLiveSkipsStubRequiringScenarios` in `lib/protocols/conformance/executor/runner_test.go` appends to the package-level `registered` slice and pops it back in `t.Cleanup`, which the project rule forbids as shared mutable state.

Four tests in `lib/protocols/conformance/executor/callback_receiver_test.go` duplicate a proof: `TestParseCallbackBody_RejectsAllThreeOutcomes` and `TestParseCallbackBody_RejectsReservedEventsField` re-run branches that `TestParseCallbackBody_RejectsMultipleOutcomes` and `TestParseCallbackBody_RejectsUnknownTopLevelField` already prove; `TestReceiver_LateCallback_AfterConsumptionRejectedNotDelivered` keys on the same `delivered[ackID]` flag as `TestReceiver_DuplicateCallback_RejectedNotDelivered`; and `TestReceiver_HandleSuccessReturns204` asserts the status the shared `postCallback` helper already asserts in every other receiver test.

`lib/services` carries the largest determinism cluster.

Six tests break the determinism rule:

- Four in `lib/services/executors/claude-agent/agentrun_test.go` (`TestRunAgentSilenceTimeout`, `TestRunAgentSilenceTimeoutFiresWithOpenToolUse`, `TestRunAgentToolUseTimeout`, `TestRunAgentToolUseEndClearsOpenToolUseBeforeItTimesOut`) pass only when 100 to 400 milliseconds of real time elapse, because the silence and tool-use timeouts in `agentrun.go` poll a bare `time.Now()` with no injected `Clock`. The fix is in the product, not the tests.
- `TestRunAgentMcpServersReachSpawnAcrossTransports` in the same file registers into the package-level `moduleRegistry` and never deregisters, where the sibling `moduleloopback_test.go` helper does both under a mutex with `t.Cleanup`.
- `TestHttpNode_429ParksWithResumeAtAndAutoWakes` in `lib/services/executors/http-node/server_test.go` brackets the expected resume time between two bare `time.Now()` reads with a 2-second tolerance, where `parseRetryAfter` already accepts an injected `now`.

One test proves nothing: `TestTick_PollsDueWatchesConcurrently_OneSlowWatchDoesNotBlockAnother` in `lib/services/sensors/sensor-http/sensor_test.go` has no assertion that can fail; a serialization regression would hang it, and the run watchdog reports a hang as inconclusive, never as red.

Six tests duplicate a proof: `TestSplitScope_ListTraversalAndDuplicateKeysCollapseToDistinctScopes` in `lib/services/claim_producers/filesystem/server/server_test.go` (covered by `TestSplitScope_ListShape`); `TestPattern_QueueMode_AutoRefresh` and `TestPattern_StagePromote` in `lib/services/claim_producers/filesystem/store/patterns_test.go` (covered by `TestOnDrain_SinglePass` and `TestAction_PopAndMove_FolderRenamed`); `TestValidator_RejectsNullCommit` in the same package (covered by `TestValidator_RejectsMissingFields`); `TestOneFireWindowPostsOneEnvelopeNamingItsSubscription` in `lib/services/sensors/sensor-cron/deterministic_idempotency_key_test.go` (covered by `TestTick_FiresDueSubscriptionAndAdvances`); and `TestSubscribe_ResyncReloadsWatermarkAfterRestart` in `lib/services/sensors/sensor-webhook/state_db_test.go` (covered by `TestReconcile_RestartRehydratesWatermarkAndBindingsLiveAgain`).

## Candidates

- Remediate the thirty-three in one sprint work item: rewrite the wall-clock wait onto the event-driven form its sibling uses, give each vacuous test an assertion on the behavior its name claims or delete it, inject the `Clock` into the claude-agent timeout loop so its four tests can drive time, and delete each redundant test, folding any stray assertion it alone carries into the test that keeps the proof.
- Extend the wall-clock lint to read a `context.WithTimeout` whose deadline feeds a verdict and a test that writes a package-level variable, so the ratchet catches the two classes the audit found by reading.
- Run the same reading audit over the root module before planning remediation, so one sprint carries the whole population.
