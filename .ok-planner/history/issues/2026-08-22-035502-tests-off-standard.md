---
issue: tests-off-standard
kind: human
category: enforcement-gap
artifacts:
  - decision:test-wallclock-lint-ratchet
  - decision:conformance-suite-per-protocol
status: promoted
sprint: 2026-08-23-row-bytes-outbox-and-log-kinds.md
opened: 2026-08-22T03:55:02Z
---

# Fifty-five tests break the testing standard

## Problem

Fifty-five test functions across the four Go modules break the testing standard at `.ok-plumbline/docs/testing.md`: sixteen reach their verdict from elapsed time or shared state, ten prove nothing, and twenty-nine prove a behavior another test in the same package already proves. A reading audit over every one of the 4,079 test functions in the tree found them; its ledger, one line per test with the evidence for its verdict, is at `.ok-planner/workbench/2026-08-22-test-audit-ledger.tsv`. The audit judged redundancy within one package only, so the twenty-nine is a floor: a scenario under `test/scenarios/` that re-proves a unit behavior, or a postgres test that mirrors its sqlite twin, was not counted.

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

The root module carries twenty-two.

Six tests break the determinism rule:

- `TestAgentStopEscalatesToSigkillWhenProcessIgnoresSigterm` in `cmd/rimsky/cli/agent_test.go` shrinks a package-level grace timeout to 200 milliseconds so the `for time.Now().Before(deadline)` loop in `agent.go` escalates on real elapsed time.
- `TestWaitForControlAPIReady_DeadlineExceeded` in `cmd/rimsky/cli/compose/launcher_test.go` passes only when a real 150-millisecond `context.WithTimeout` expires against a server that always answers 503; `WaitForControlAPIReady` takes no `Clock`.
- `TestDrain_SIGTERMThenSIGKILLChildren_BoundedTime` in `cmd/rimsky/cli/compose/shutdown_test.go` spawns a child that ignores SIGTERM, so the 5-second `childGraceWindow` timer in `shutdown.go` must elapse before the SIGKILL it asserts.
- `TestProducerVerbDispatcher_RunDeliversOnKickWithoutClockAdvance` in `lib/runtime/producer_verb_outbox_test.go` busy-polls `outbox.ListAll` with `runtime.Gosched()` in an unbounded loop with no declared wait class.
- `TestHostAgentControlPlaneDemo` in `test/scenarios/host_agent_control_plane_demo_test.go` is synchronous itself, but the demo script it runs polls a `date +%s` deadline with `sleep 0.1` in `test/fixtures/demos/host-agent-control-plane-demo.sh`, so the verdict flips on a loaded machine.
- `TestDelayRespectsContextCancellation` in `test/support/executors/stub/stub_test.go` races a 50-millisecond caller deadline against a 500-millisecond stub delay.

Six tests prove nothing:

- `TestClaimProducerObsCapabilities` in `cmd/rimsky-host-agent-proxy/claim_producer_handler_test.go` and `TestNoOpLifecycleMethods` in `cmd/rimsky-host-agent-proxy/lifecycle_handler_test.go` assert only `err == nil`; the lifecycle methods are literal `return &LifecycleAck{}, nil` stubs.
- `TestResources_Read_LimitCappedAtMax` in `lib/control/controlapi/mcp_resources_test.go` never checks that the limit was clamped.
- `TestUnifiedStack_DrainEmptyIsNoOp` in `lib/control/launch/unified_test.go` calls `Drain` on an empty stack and asserts nothing.
- `TestBootstrapTokenLifetimeOutlastsLeafRenewalInterval` in `lib/runtime/hostagent/local_http_test.go` compares two constants the test itself derives from `pki.LeafTTL`.
- `TestOpenErrorRollsBackRimskySideInsertsDelegated` in `test/scenarios/claim_producers/open_rollback_test.go` is a `t.Skip` that points at another test.

Ten tests duplicate a proof: `TestClaimHoldersRoute_EmptyList` in `lib/control/controlapi/app_test.go` (covered by `admin_routes_test.go::TestClaimHoldersRoute`); `TestInstancePauseResume_RedundantTransitionsReturn409` in `lib/control/controlapi/instances_pause_idempotency_test.go` (covered by `TestPauseResume_NonIdempotentVerbs409`); `TestLifecycleReconciler_RunScopeTerminalPrecedesInstanceTerminated` in `lib/control/controlapi/lifecycle_fanout_after_commit_test.go` (covered by `TestLifecycleReconciler_RowFoundRPCSucceedsRowDeleted`); `TestCreateMessage_DeclaredTypeAccepted` in `lib/control/controlapi/messages_test.go`; `TestSendCascadeMessageInTx_ReplayDoesNotDoubleInsertEnvelope` in `lib/runtime/runner_send_message_test.go` (covered by `TestSendCascadeMessageInTx_IdempotentOnNodeAndFrame`); `TestSubgraphParentSuccessCascade_ReturnsInternals` in `lib/runtime/subgraph_dispatch_test.go` (a pass-through whose target test already asserts the count); `TestAcquireUnavailablePass` in `test/scenarios/acquire_unavailable_pass_test.go` (covered by `TestAcquirePassSubscribedMonitorRuns`); `TestGrantScope_TemplateTagEnforced` in `test/scenarios/auth/grant_scope_test.go` (covered by `grant_scope_lifecycle_test.go::TestGrantScope_TemplateRegister_HashForm`); `TestRotation_DualActiveAndSweep` in `test/scenarios/auth/lifecycle_test.go` (covered inside `TestLifecycle_APIKeyManagement_AcceptanceWalk`); and `TestFanoutAggregation_PolicyTable` in `test/scenarios/run_tree/fanout_aggregation_test.go` (its threshold subtests repeat `error_policy_threshold_test.go`).

## Candidates

- Remediate the fifty-five in one sprint work item: rewrite the wall-clock wait onto the event-driven form its sibling uses, give each vacuous test an assertion on the behavior its name claims or delete it, inject the `Clock` into the claude-agent timeout loop, the agent-stop escalation, the control-API readiness wait, and the compose drain so their tests can drive time, and delete each redundant test, folding any stray assertion it alone carries into the test that keeps the proof.
- Run a second reading pass grouped by behavior rather than by file — the event kind a test waits on, the route it hits, the concept it cites — to count cross-package and cross-backend duplication, which this audit left unmeasured.
- Extend the wall-clock lint to read a `context.WithTimeout` whose deadline feeds a verdict and a test that writes a package-level variable, so the ratchet catches the two classes the audit found by reading.

## Ruling

Remediate all fifty-five in one sprint work item, per the ledger: rewrite each wall-clock wait onto the event-driven form, give each vacuous test an assertion on the behavior its name claims or delete it, inject the `Clock` into the claude-agent timeout loop, the agent-stop escalation, the control-API readiness wait, and the compose drain, and delete each redundant test, folding any stray assertion into the test that keeps the proof. Extend the wall-clock lint to read a `context.WithTimeout` whose deadline feeds a verdict and a test that writes a package-level variable. The owner does not take the behavior-grouped second reading pass; it is a new audit, not work. The owner ruled live on 2026-08-23.
