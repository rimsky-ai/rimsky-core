# Rimsky Verification Report

**Generated 2026-06-02.** Proof that every documented feature of rimsky-core is
code-complete, exercised by a test that drives the real system, and passing.

## Verdict

**PASS.** Every documented concept (70 of 71) is exercised by a test that drives
the real running system and asserts an observable outcome; the one exception
(`module-layout`) is a build/lint-enforced layout rule with no runtime behavior.
The complete test suite — all four Go modules plus the TypeScript executor — passes
against real Postgres (testcontainers) and the locally-built service images, and
the `-race` detector is **clean on every concurrent-sensitive path** (no data
races). Closing the coverage gaps required **no implementation fixes**: the
behaviors were correctly wired, only unproven. The project is verified working
end to end.

## What this is and how it was produced

This report answers one question: **is every part of the project actually
tested and working as designed?** It was produced in three stages, all against
the real system (no shape tests, no sampling):

1. **Feature trace.** Every concept in the catalog (`.ok-planner/design/concepts/`)
   was traced to the test(s) that prove it, by reading the concept's Definition /
   Boundaries / Invariants, the code that enforces it (`@concept:` annotations),
   and the test bodies — then classifying coverage as **behavioral** (a test
   drives the concept's real runtime behavior and asserts an observable outcome:
   a testcontainers Postgres scenario, a real-router+Postgres handler test, a real
   persistence round-trip, a conformance run, a real executor/agent process),
   **shape** (only struct/proto/pure-function assertions), or **n/a** (no runtime
   behavior to test).
2. **Full real suite.** Every test across all four Go modules (root,
   `lib/foundation`, `lib/protocols`, `lib/services`) plus the TypeScript executor
   was run for real — testcontainers Postgres for the scenario/persistence/
   integration tests, the bundled service images built and consumed by the
   services harness. Plus a `-race` pass on the concurrent-sensitive paths.
3. **Gap closure.** Every gap the trace found was closed with a real behavioral
   test (and the implementation fixed if the new test revealed a bug).

## Coverage summary

- **70 of 71 concepts: behavioral** — driven end to end by a
  test that exercises real runtime behavior.
- **1 concept: n/a** — `module-layout`, which has no runtime behavior:
  its invariants (the import-boundary / module-purity rules) are enforced by the
  `.golangci.yml` depguard block at `make lint` and by the `go.work` module graph
  at build time, not by any Go test.
- **0 concepts shape-only or missing** after the closures below.

## Gaps closed in this verification pass

The trace surfaced one concept-level gap and a few sub-property gaps. All were
closed with real behavioral tests; none required an implementation fix (the
behaviors were correctly wired, only unproven):

| Concept | Gap | Closure |
|---|---|---|
| `named-lock` | Capacity-limit enforcement (`limit:1` mutex / `limit:N` semaphore; increment/decrement at terminal, `@blessed-invariant 2`) had no test driving real contention. | `test/scenarios/locks/named_lock_limit_test.go` — two/N+1 node-runs contend against real Postgres; asserts mutual exclusion, saturation bail, and release-at-terminal. Coupling proven by mutation (disabling enforcement hangs the test). |
| `write-semantics` | Per-value concurrency discrimination — `staged_async` reader coexistence and `blocking_async` serialization — was only pure-function tested; only `sync` was driven end to end. | `test/scenarios/locks/write_semantics_coexistence_test.go` — concurrent same-scope reader-vs-writer acquisition asserts coexistence (two active rows) under `staged_async` and serialization (reader bails, re-acquirable) under `blocking_async`. Coupling proven by mutation of `isSync`. |
| `observability` | `expected_attributes_schema` (the former `userdata_schema`) validation seam — the resolver feeding registration + dispatch validators from the discovery cache — had zero coverage. | `lib/control/observability/expected_attributes_schema_resolver_test.go` — drives the real resolver + both enforcement points (registration `ValidateTemplate`, dispatch `attributes.Validate`) with conforming and violating inputs. |
| `rimsky-yml` | Loader rejection of retired config aliases (`stores:`, `write_semantics:`, `write_semantics_envelope:`) had no test. | `lib/control/config/retired_aliases_test.go` — each retired alias is fed through the real loader and asserted rejected with a clear error; valid spelling still loads. |
| `cascade-graph` | Concept doc claimed routes mount at "bare, unversioned paths"; they actually mount under the control API's versioned prefix. | Doc corrected (path-free) with a dated Notes entry. |

## Documented non-coverage (intentional or inherent — not test gaps)

These are deliberate or inherent limits, recorded here so nothing is hidden:

- **`atomic-staging` — producer-side staging-schema swap is not shipped.** The
  Commit/Abandon verb dispatch is proven end to end; the producer-side schema
  swap is intentionally unbuilt (per the concept's own Notes), so there is no
  behavior to test there yet.
- **`inertness` — negative discipline.** "Rimsky never logs or transforms claim
  carrier bytes" is a negative property no test can fully assert; the positive
  side (byte-opaque round-trip through real backends; the one sanctioned matcher
  read site) is driven behaviorally.
- **`role-template` — expanded-grant body on the wire.** Role expansion is proven
  behaviorally via `CheckGrant` over the expanded grant; the create-key e2e
  asserts route/exit/auth but not the expanded grant body on the wire (a
  redundant assertion over already-covered behavior).

## Full concept → proving-test map

| Concept | Coverage | Proving test(s) |
|---|---|---|
| `advisory-lock` | behavioral | `lib/graph/scheduler/scheduler_test.go::TestScheduler_AdvisoryLockBlocksSecondReplica`<br>`lib/foundation/persistence/conformance/conformance_test.go::TestConformancePostgres/CoordinatorSchedulerTick` |
| `anonymous-mode` | behavioral | `test/scenarios/auth/lifecycle_test.go::TestBootstrap_AnonymousToAuthenticated`<br>`test/scenarios/auth/lifecycle_test.go::TestRevokeGuard_RefuseLastKey` |
| `api-key` | behavioral | `test/scenarios/auth/lifecycle_test.go::TestRotation_DualActiveAndSweep`<br>`test/scenarios/auth/lifecycle_test.go::TestPermissionGrants_ReadOnlyDenyOnWrite` |
| `asset` | behavioral | `lib/control/controlapi/assets_test.go::TestAssetEndpoints_ListSurfacesDurableCommittedRows`<br>`lib/control/controlapi/assets_test.go::TestAssetEndpoints_DeleteReleasesAndDeletes` |
| `atomic-staging` | behavioral | `lib/services/test/scenarios/atomic_staging/pg_verifier_commit_abandon_test.go::TestAtomicStaging_VerifierSuccess_DrivesCommit`<br>`lib/services/test/scenarios/atomic_staging/pg_verifier_commit_abandon_test.go::TestAtomicStaging_VerifierFailure_DrivesAbandon` |
| `attribute` | behavioral | `test/scenarios/attributes/substitution_dispatch_test.go::TestParamsSubstitutionAtDispatch`<br>`test/scenarios/per_run_attributes/substitution_test.go::TestPerRunAttributes_DownstreamReadsThisFrame` |
| `auto-terminal` | behavioral | `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go::TestAutoTerminalAggregateCommitEndToEnd`<br>`test/scenarios/asset/durable_lifetime_e2e_test.go::TestDurableLifetimeE2E` |
| `backfill` | behavioral | `lib/control/controlapi/backfills_test.go::TestBackfills_CreateRejectsFanOutNotWiredForOverride`<br>`lib/control/controlapi/backfills_test.go::TestBackfills_CreateListShowCancel` |
| `blob-backend` | behavioral | `lib/foundation/persistence/postgres/blob_largeobject_test.go::TestPgLargeObjectBackend`<br>`lib/foundation/persistence/sqlite/node_attributes_spill_test.go::TestNodeAttributesSpillRoundtrip` |
| `breakpoint` | behavioral | `test/scenarios/breakpoints/pause_resume_happy_path_test.go::TestPauseResumeHappyPath`<br>`test/scenarios/breakpoints/resume_with_overlay_test.go` |
| `cancel-siblings` | behavioral | `test/scenarios/lineage/force_cancelled_lineage_test.go::TestForceCancelledLineage_CancelSiblingsEmitsForceCancelledRows` |
| `cascade` | behavioral | `test/scenarios/messages/message_cascade_e2e_test.go::TestMessageCascadeE2E_SubscriberFlipsStale` |
| `cascade-graph` | behavioral | `lib/control/observability/handler_test.go::TestHandler_SystemSummary_DispatchCounts`<br>`lib/control/observability/handler_test.go::TestHandler_ListFrames_Empty` |
| `claim` | behavioral | `test/scenarios/stores/scope_claim_test.go::TestScopeClaimEndToEnd` |
| `claim-co-holdership` | behavioral | `test/scenarios/verifier/holds_only_auto_terminal_e2e_test.go::TestHoldsOnlyAutoTerminal`<br>`test/scenarios/verifier/co_holding_drives_promotion_test.go` |
| `claim-handle` | behavioral | `lib/foundation/persistence/conformance/conformance_test.go::TestConformancePostgres`<br>`test/scenarios/asset/durable_lifetime_e2e_test.go::TestDurableLifetimeE2E` |
| `claim-lifetime` | behavioral | `test/scenarios/asset/durable_lifetime_e2e_test.go::TestDurableLifetimeE2E` |
| `claim-producer` | behavioral | `cmd/rimsky/conformance_claimproducer_test.go::TestClaimProducerConformance_StubStore`<br>`test/scenarios/stores/scope_claim_test.go::TestScopeClaimEndToEnd` |
| `claim-scope` | behavioral | `test/scenarios/locks/claim_scope_conflict_race_test.go::TestClaimScopeClaimRace_OneAcquirerWins`<br>`lib/foundation/persistence/conformance/conformance_test.go::TestConformancePostgres` |
| `claim-tree` | behavioral | `test/scenarios/forensics/fanout_post_mortem_test.go::TestFanoutPostMortem_MixedOutcomesEmitFullForensicsTrail`<br>`test/scenarios/lineage/force_cancelled_lineage_test.go` |
| `conformance` | behavioral | `cmd/rimsky/conformance_claimproducer_test.go::TestClaimProducerConformance_StubStore`<br>`cmd/rimsky/conformance_dataprocessing_test.go::TestDataProcessingConformance_StubStore` |
| `control-api` | behavioral | `lib/control/controlapi/actions_test.go::TestRegistryRoutesAreActuallyGated`<br>`lib/control/controlapi/actions_test.go::TestRegistryCoversRouter` |
| `data-processing` | behavioral | `test/scenarios/leaf_candidate_handle_e2e_test.go::TestLeafCarriesCandidateHandle` |
| `delegation` | behavioral | `test/scenarios/subgraph_exit_carry_e2e_test.go::TestSubgraphExitCarryE2E`<br>`test/scenarios/subgraph/entry_absorption_test.go::TestEntryAbsorption_MarkerEmittedOnCallingNode` |
| `discovery-cache` | behavioral | `lib/control/observability/handshake_test.go::TestRefreshLoop_HealsUnreachable`<br>`lib/control/observability/handshake_test.go::TestRunHandshake_UnreachableExecutor_NoError` |
| `dry-run` | behavioral | `test/scenarios/auth/dry_run_coverage_test.go::TestDryRunCoverage_AllWriteActions`<br>`lib/control/controlapi/auth_middleware_test.go::TestGate_DryRunFlagSetsModeAndReadExecutes` |
| `error-policy` | behavioral | `test/scenarios/retry_loop_cap_test.go::TestRetryLoopCapForcesGiveUp`<br>`test/scenarios/give_up_test.go::TestGiveUp` |
| `event-log` | behavioral | `test/scenarios/auth/audit_durability_test.go::TestAuditDurability_NoDropsUnderConcurrentLoad`<br>`test/scenarios/auth/audit_read_test.go::TestAuditRead_FilterAndGate` |
| `executor` | behavioral | `test/scenarios/agentic_executor_async_handoff_test.go::TestAgenticExecutorAsyncHandoff`<br>`test/scenarios/happy_path_executor_test.go::TestHappyPathExecutor` |
| `fan-out` | behavioral | `test/scenarios/fanout_success_cascade_e2e_test.go::TestFanOutSuccessCascadeE2E`<br>`test/scenarios/child_partition_key_e2e_test.go::TestChildPartitionKeyBinds` |
| `frame` | behavioral | `test/scenarios/frame_resolution/serial_queue_each_invalidate_one_frame_test.go::TestSerialQueueEachInvalidateOneFrame`<br>`test/scenarios/frame_resolution/per_instance_ordering_invariant_test.go::TestPerInstanceOrderingInvariant_DirectSQL` |
| `graph` | behavioral | `test/scenarios/subgraph_internal_cascade_e2e_test.go::TestSubgraphInternalCascadeE2E`<br>`lib/graph/node/template_validator_graphs_test.go::TestCanonicalizeGraphs_RejectMissingMain` |
| `host-agent` | behavioral | `test/scenarios/host_agent_late_bind_executor_test.go::TestHostAgentLateBindExecutorHappyPath`<br>`test/scenarios/host_agent_reap_test.go::TestHostAgentReapOnRunScopeTerminal` |
| `host-agent-proxy` | behavioral | `test/scenarios/host_agent_late_bind_executor_test.go::TestHostAgentLateBindExecutorHappyPath`<br>`test/scenarios/host_agent_reap_test.go::TestHostAgentReapOnRunScopeTerminal` |
| `inertness` | behavioral | `lib/foundation/persistence/blob_roundtrip_test.go::TestBlobRoundtripBackends`<br>`test/scenarios/attribute_overrides_match_overlay_flat_template_graph_resolution_e2e_test.go::TestAttributeOverridesMatchOverlayFlatTemplateGraphResolution_ResolvesToMain` |
| `instance` | behavioral | `lib/control/controlapi/instances_test.go::TestTerminateInstance_ForceFailsRunningNode`<br>`lib/foundation/persistence/conformance/conformance_test.go::TestConformancePostgres` |
| `invalidate` | behavioral | `test/scenarios/cascade_invalidate_test.go::TestCascadeInvalidate`<br>`lib/control/controlapi/messages_test.go::TestMessages_PostListGet` |
| `lifecycle-subscriber` | behavioral | `test/scenarios/lifecycle/lifecycle_e2e_test.go::TestLifecycleE2E_FullSequence`<br>`lib/control/controlapi/instance_terminator_test.go::TestInstanceTerminator_RowFoundRPCSucceedsRowDeleted` |
| `lineage` | behavioral | `lib/services/subscribers/openlineage/subscriber_test.go::TestSubscriber_EndToEnd_PollsAndEmits`<br>`lib/foundation/persistence/conformance/conformance_test.go::TestConformancePostgres` |
| `lineage-record` | behavioral | `test/scenarios/lineage/claim_abandon_lineage_test.go::TestClaimAbandonLineage_NaturalAbandonEmitsAbandonedOutcome`<br>`lib/services/subscribers/openlineage/subscriber_test.go::TestSubscriber_EndToEnd_PollsAndEmits` |
| `message` | behavioral | `test/scenarios/messages/message_cascade_e2e_test.go::TestMessageCascadeE2E_SubscriberFlipsStale`<br>`lib/control/controlapi/messages_test.go::TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting` |
| `module-layout` | n/a | — |
| `named-event` | behavioral | `test/scenarios/conformance_events_test.go::TestConformanceEvents`<br>`test/scenarios/on_event_test.go::TestOnEventGRPCStreamPath` |
| `named-lock` | behavioral | `test/scenarios/locks/named_lock_limit_test.go::TestNamedLockMutexEnforcesMutualExclusion`<br>`test/scenarios/locks/named_lock_limit_test.go::TestNamedLockSemaphoreSaturatesAtLimit` |
| `node` | behavioral | `test/scenarios/cascade_invalidate_test.go::TestCascadeInvalidate`<br>`lib/foundation/cascade/state_test.go::TestRunningToRunningUnderDispatchClaimedIsRejected` |
| `node-run` | behavioral | `lib/foundation/persistence/sqlite/queue_park_test.go::TestSQLiteParkResumeRoundTrip`<br>`test/scenarios/locks/node_run_phase_test.go::TestNodeRunPhaseAdvancesOnClaim` |
| `node-subscription` | behavioral | `test/scenarios/cascade_invalidate_test.go::TestCascadeInvalidate`<br>`test/scenarios/frame_coalesce_self_invalidate_test.go::TestFrameCoalesceSelfInvalidate` |
| `observability` | behavioral | `lib/control/observability/expected_attributes_schema_resolver_test.go::TestExpectedAttributesSchemaResolver_BehavioralValidation`<br>`lib/services/stores/postgres/server/observability_test.go::TestObservability_StreamClaim_Postgres_AfterTerminal` |
| `orphan-reaper` | behavioral | `test/scenarios/orphaned_claim_test.go::TestOrphanedClaim`<br>`lib/foundation/persistence/conformance/conformance_test.go::TestConformancePostgres/OrphanCutoffTime` |
| `parked-state` | behavioral | `test/scenarios/parked_lifecycle_test.go::TestParkedLifecycleResumeOnDeadline`<br>`test/scenarios/parked_lifecycle_test.go::TestParkedLifecycleResumeOnExternalInvalidate` |
| `permission` | behavioral | `test/scenarios/auth/lifecycle_test.go::TestPermissionGrants_ReadOnlyDenyOnWrite`<br>`test/scenarios/auth/lifecycle_test.go::TestMCPSkin_RequiresMCPReadGate` |
| `persistence-database` | behavioral | `lib/foundation/persistence/conformance/conformance_test.go::TestConformancePostgres`<br>`lib/foundation/persistence/conformance/conformance_test.go::TestConformanceSQLite` |
| `publisher` | behavioral | `cmd/rimsky/conformance_publisher_test.go::TestPublisherConformance_FixtureCron` |
| `publisher-subscription` | behavioral | `lib/control/controlapi/messages_test.go::TestCreateMessage_SenderKindPublisherActiveSubscriptionSucceeds`<br>`lib/control/controlapi/messages_test.go::TestCreateMessage_SenderKindPublisherWrongInstanceForbidden` |
| `replica` | behavioral | `lib/services/sensors/sensor-cron/multi_replica_test.go::TestMultiReplica_TwoInProcessInstancesEachFireIndependently`<br>`lib/services/sensors/sensor-cron/multi_replica_test.go::TestSingleReplica_FiresOnceWhenSubscriptionTickFires` |
| `rimsky` | behavioral | `cmd/rimsky/cli/auth_subcommands_test.go::TestAuthCreate_HappyPath`<br>`cmd/rimsky/cli/instances_test.go::TestRunInstanceStatus_KeyResolution` |
| `rimsky-yml` | behavioral | `lib/control/config/retired_aliases_test.go::TestLoadRimskyConfigYAML_RejectsRetiredAliases`<br>`lib/services/test/harness/rimsky_test.go::TestBringUpRimsky_HealthGreen` |
| `role-template` | behavioral | `cmd/rimsky/cli/roles/audit_read_coverage_test.go::TestRolesCoverAuditRead`<br>`cmd/rimsky/cli/auth_common_test.go::TestApplyGrantPatches_AddRemove` |
| `run-scope` | behavioral | `lib/foundation/persistence/conformance/run_scope_lifecycle.go::testRunScopeFanoutPartitionUniqueness`<br>`lib/foundation/persistence/conformance/run_scope_lifecycle.go::testRunScopeAffirmAfterClose_ErrRunScopeClosed` |
| `sensor` | behavioral | `lib/services/sensors/sensor-cron/sensor_test.go::TestTick_FiresDueSubscriptionAndAdvances`<br>`lib/services/sensors/sensor-http/sensor_test.go::TestTick_PollsAndPushesOnChange` |
| `service` | behavioral | `lib/services/test/scenarios/bundled_executor_vocab_test.go::TestPostgresStores_EmitsHierarchicalErrorClasses` |
| `signal` | behavioral | `test/scenarios/no_op_commit_test.go::TestNoOpCommit`<br>`test/scenarios/breakpoints/signal_type_filter_test.go::TestSignalTypeFilter` |
| `sub-graph` | behavioral | `lib/graph/node/template_validator_graphs_test.go::TestCanonicalizeGraphs_RejectDelegateCycle`<br>`test/scenarios/subgraph_exit_carry_e2e_test.go::TestSubgraphExitCarryE2E` |
| `supervisor` | behavioral | `test/scenarios/verify_before_run_race_test.go::TestVerifyBeforeRunRace`<br>`test/scenarios/orphaned_claim_test.go::TestOrphanedClaim` |
| `tag` | behavioral | `lib/control/controlapi/tags_test.go::TestDeleteTag_DoesNotDeleteTemplate`<br>`lib/control/controlapi/tags_test.go::TestCreateTag_RejectsHashShape` |
| `template` | behavioral | `lib/control/controlapi/templates_test.go::TestTemplateRegister_Idempotent`<br>`lib/control/controlapi/templates_test.go::TestTemplateDeploy_StateTransitions` |
| `terminal-resolution` | behavioral | `test/scenarios/held_claim_acquirer_passes_test.go::TestHeldClaimAcquirerPasses`<br>`test/scenarios/no_op_commit_test.go::TestNoOpCommit` |
| `transition-reason` | behavioral | `lib/control/controlapi/instances_test.go::TestTerminateInstance_ForceFailsRunningNode`<br>`test/scenarios/state_machine_same_state_rejected_test.go::TestStateMachineSameStateRejected` |
| `validation` | behavioral | `lib/control/controlapi/validation_pipeline_test.go::TestValidationPipeline_RejectsOnError`<br>`lib/control/controlapi/validation_pipeline_test.go::TestValidationPipeline_PassesOnWarningsOnly` |
| `wait-set` | behavioral | `lib/foundation/persistence/conformance/conformance_test.go::TestConformancePostgres`<br>`test/scenarios/subscription_cascade_test.go::TestSubscriptionCascade_EligibilityRespectsMultipleSenders` |
| `write-semantics` | behavioral | `test/scenarios/locks/write_semantics_coexistence_test.go::TestWriteSemanticsStagedAsyncReaderCoexistsWithWriter`<br>`test/scenarios/locks/write_semantics_coexistence_test.go::TestWriteSemanticsBlockingAsyncSerializesReaderBehindWriter` |

## Final proof run

**Full suite** — every test, against real Postgres (testcontainers) and the
locally-built service images consumed by the services integration harness:

| Target | Result |
|---|---|
| root module (`go test ./...`) | ✅ ok |
| `lib/foundation` | ✅ ok |
| `lib/protocols` | ✅ ok |
| `lib/services` | ✅ ok |
| TypeScript executor (`npm test && npm run build`) | ✅ ok |

**Race detector** — `go test -race` on the concurrent-sensitive paths; **no data
races detected** on any path:

| Target | Result |
|---|---|
| `test/scenarios/locks/...` (incl. the new named-lock + write-semantics concurrency tests) | ✅ ok |
| `lib/graph/scheduler/...` | ✅ ok |
| `lib/foundation/persistence/postgres/...` | ✅ ok |
| `lib/runtime/...` | ✅ ok |

_Note on `lib/graph/scheduler` under `-race`: in the first batched proof run (full
suite + three race legs churning containers concurrently) this leg hit a
testcontainers Postgres **startup timeout** — `wait until ready: ... context
deadline exceeded`, a Docker-saturation symptom, **not** a data race or logic
failure. The same package passed in the non-race suite run, and re-running the
race leg in isolation passed clean (4.6s, zero data races). Environmental, not a
code defect._

---

_This report is regenerable: the concept map is derived from the feature-trace
audit; the proof results are the recorded output of `go test` across all modules
plus the `-race` pass. Both are reproducible against a Docker-enabled checkout._
