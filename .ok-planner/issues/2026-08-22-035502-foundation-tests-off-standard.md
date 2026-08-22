---
issue: foundation-tests-off-standard
kind: human
category: enforcement-gap
artifacts:
  - decision:test-wallclock-lint-ratchet
  - decision:conformance-suite-per-protocol
status: open
opened: 2026-08-22T03:55:02Z
---

# Thirteen `lib/foundation` tests break the testing standard

## Problem

Thirteen test functions under `lib/foundation` break the testing standard at `.ok-plumbline/docs/testing.md`: one reaches its verdict from elapsed time, three prove nothing, and nine prove a behavior another test in the same package already proves. A reading audit over every test function in the module found them.

The standard says a verdict never depends on elapsed time, and the project's rule adds that every admitted wait declares a class. `TestAcquireMigrationLock_HonorsContextCancel` in `lib/foundation/persistence/sqlite/advisory_locker_test.go` passes only when a bare 100-millisecond `context.WithTimeout` fires before the lock call returns, and declares no `//nolint:testwallclock-<class>`. The wall-clock lint did not catch it because the lint reads five constructs and `context.WithTimeout` is not one of them. The sibling test in the same file proves the same contention through the event-driven `awaited.Until`.

The standard says a test proves a behavior a user or a story owes. Three tests assert nothing that could fail on a behavior change:

- `TestRegistryConcurrentAddAndReadIsRaceFree` in `lib/foundation/locks/registry_test.go` and its twin of the same name in `lib/foundation/lifecycle/lifecycle_test.go` run goroutines and return after `wg.Wait()`; with `-race` banned from the suite, only a runtime map panic could fail them.
- `TestSQLiteUnifiedStackMaxOpenConnsAvoidsBeginStarvation` in `lib/foundation/persistence/sqlite/database_test.go` asserts a constant is at least 2 and opens no transaction.

The standard says to remove a test that duplicates a proof. Nine do:

- Four in `lib/foundation/cascade/state_test.go` (`TestFreshAndFailedAreTerminal`, `TestRunningToRunningUnderDispatchClaimedIsRejected`, `TestNextState_AggregateSettlementIsIllegalForLeafRuns`, `TestNextState_ReleaseReturnsAClaimedRunToStale`) each assert a cell of the exhaustive matrix `TestTransitionTable` already asserts.
- `TestModeCoexistsSymmetric` in `lib/foundation/locks/conflict_test.go` proves symmetry that `TestModeCoexistsMatrix` already proves by asserting both orderings.
- Four in `lib/foundation/persistence/blob_test.go` (`TestMemoryBackend`, `TestMemoryBackendReadRangeOutOfBounds`, `TestFilesystemBackend`, `TestFilesystemBackendReadRangeOutOfBounds`) repeat checks the shared blob conformance suite already runs against both backends through `TestBlobRoundtripBackends`.

## Candidates

- Remediate the thirteen in one sprint work item: rewrite the wall-clock wait onto the event-driven form its sibling uses, give each vacuous test an assertion on the behavior its name claims or delete it, and delete each redundant test, folding any stray assertion it alone carries into the test that keeps the proof.
- Extend the wall-clock lint's construct list with a `context.WithTimeout` whose deadline feeds a verdict, so the ratchet catches the class the audit found by reading.
- Run the same reading audit over `lib/protocols`, `lib/services`, and the root module before planning remediation, so one sprint carries the whole population.
