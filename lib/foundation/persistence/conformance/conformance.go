// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package conformance is the cross-driver test suite. Both Postgres and
// SQLite drivers must pass every test here. Run via the per-driver
// wrappers in conformance_test.go.
//
// Spec: §9.1.
package conformance

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// RawQueryRow is the read-side companion to the per-driver rawExec
// helper. Each entry is one row (column name → value as scanned by the
// driver's sql layer). Used by tests that need to assert on columns
// not surfaced by the application-layer projections (e.g. claimed_by
// on individual rimsky_node_runs rows when sibling fan-out shares a
// node_id and the LATERAL/ROW_NUMBER projection in nodeSelect collapses
// them).
type RawQueryRow = map[string]any

// Suite runs every conformance check against the driver returned by
// factory. Each subtest is independent; factory is called once per
// subtest so each gets a fresh DB.
//
// rawExec is a per-driver test-helper that runs raw SQL against the
// driver's underlying connection. Used by tests that need to bypass
// the application-layer Create paths (e.g. the migration-backfill
// case for instances.attribute_overrides). The helper is responsible
// for translating the question-mark placeholders into the driver-
// native style (`$N` for postgres, `?` for sqlite). rawExec is
// REQUIRED: several tests call it unconditionally (the
// instances.attribute_overrides migration-backfill case,
// FrameSettlement/StuckFrames' last_progress_at backdating) and would
// nil-panic without it. Both in-tree drivers supply one.
//
// rawQuery is the read-side companion. Returns the rows as
// []RawQueryRow with column-name keys. Same `?` placeholder convention
// as rawExec; same per-driver translation responsibility.
func Suite(
	t *testing.T,
	factory func(*testing.T) persistence.Database,
	rawExec func(t *testing.T, d persistence.Database, sql string, args ...any),
	rawQuery func(t *testing.T, d persistence.Database, sql string, args ...any) []RawQueryRow,
) {
	t.Helper()
	t.Run("DispatchClaimRelease", func(t *testing.T) { testDispatchClaimRelease(t, factory(t)) })
	t.Run("VerifyBeforeRunRead", func(t *testing.T) { testVerifyBeforeRunRead(t, factory(t)) })
	t.Run("MigrationIdempotency", func(t *testing.T) { testMigrationIdempotency(t, factory(t)) })
	t.Run("CoordinatorSchedulerTick", func(t *testing.T) { testCoordinatorSchedulerTick(t, factory(t)) })
	t.Run("ForeignKeyCascade", func(t *testing.T) { testForeignKeyCascade(t, factory(t)) })
	t.Run("ClaimScopeByteEquality", func(t *testing.T) { testClaimScopeByteEquality(t, factory(t)) })
	t.Run("OrphanCutoffTime", func(t *testing.T) { testOrphanCutoffTime(t, factory(t)) })
	t.Run("TxAtomicity", func(t *testing.T) { testTxAtomicity(t, factory(t)) })
	t.Run("AcquisitionTxAtomicity", func(t *testing.T) { testAcquisitionTxAtomicity(t, factory(t)) })
	t.Run("HeldClaimAutoTerminalSerialization", func(t *testing.T) { testHeldClaimAutoTerminalSerialization(t, factory(t)) })
	t.Run("SortOrderCoordination", func(t *testing.T) { testSortOrderCoordination(t, factory(t)) })
	t.Run("PublisherSubscriptionLifecycle", func(t *testing.T) { testPublisherSubscriptionLifecycle(t, factory(t)) })
	t.Run("QueueInTxAndDispatchNode", func(t *testing.T) { testQueueInTxAndDispatchNode(t, factory(t)) })
	t.Run("SelectCandidatesSkipsPausedInstances", func(t *testing.T) { testSelectCandidatesSkipsPausedInstances(t, factory(t)) })
	t.Run("SelectCandidatesKeysetCursor", func(t *testing.T) { testSelectCandidatesKeysetCursor(t, factory(t)) })
	t.Run("QueueRebindRunFrameInTx", func(t *testing.T) { testQueueRebindRunFrameInTx(t, factory(t)) })
	t.Run("ClaimHandlesUpdateClaimScope", func(t *testing.T) { testClaimHandlesUpdateClaimScope(t, factory(t)) })
	// @deliberate: spec 2026-05-22-fan-out-safety-scope-first-design retired
	// NodesMarkStaleForCascade conformance — MarkStaleForCascade is now keyed
	// on runID (pure UPDATE); allocation moved to AffirmNodeRunRow. The
	// shaped-from-nodeID + bool-return-of-inserted contract this test pinned
	// is gone. Replacement coverage: AffirmNodeRunRow conformance below.
	t.Run("NodesListRunningBySupervisor", func(t *testing.T) { testNodesListRunningBySupervisor(t, factory(t)) })

	// @deliberate: spec 2026-05-22-fan-out-safety-scope-first-design — RunScope-first
	// conformance group (Tasks 28–31, 55).
	t.Run("RunScopeLifecycle", func(t *testing.T) {
		t.Run("CreateMainAndChild", func(t *testing.T) { testRunScopeCreate_MainAndChild(t, factory(t)) })
		t.Run("CloseStampsClosedAt", func(t *testing.T) { testRunScopeClose_StampsClosedAt(t, factory(t)) })
		t.Run("AffirmAfterCloseErrors", func(t *testing.T) { testRunScopeAffirmAfterClose_ErrRunScopeClosed(t, factory(t)) })
		t.Run("FanoutPartitionUniqueness", func(t *testing.T) { testRunScopeFanoutPartitionUniqueness(t, factory(t)) })
		t.Run("ListParentChain", func(t *testing.T) { testRunScopeListParentChain(t, factory(t)) })
	})
	t.Run("AffirmNodeRunRow", func(t *testing.T) {
		t.Run("InsertsWhenNoInFlight", func(t *testing.T) { testAffirmNodeRunRow_InsertsWhenNoInFlight(t, factory(t)) })
		t.Run("Idempotent", func(t *testing.T) { testAffirmNodeRunRow_Idempotent(t, factory(t)) })
		t.Run("ErrorsOnClosedScope", func(t *testing.T) { testAffirmNodeRunRow_ErrorsOnClosedScope(t, factory(t)) })
		t.Run("NoReturnValueDependency", func(t *testing.T) { testAffirmNodeRunRow_NoReturnValueDependency(t, factory(t)) })
		t.Run("AffirmThenRead", func(t *testing.T) { testAffirmThenRead(t, factory(t)) })
	})
	t.Run("RunInFlightLookup", func(t *testing.T) {
		t.Run("SingleRowPerScopePerNode", func(t *testing.T) { testInFlightLookup_SingleRowPerScopePerNode(t, factory(t)) })
		t.Run("NoFalsePositiveAcrossScopes", func(t *testing.T) { testInFlightLookup_NoFalsePositiveAcrossScopes(t, factory(t)) })
		t.Run("ReturnsNoneWhenAbsent", func(t *testing.T) { testInFlightLookup_ReturnsNoneWhenAbsent(t, factory(t)) })
	})
	t.Run("ListInFlightRunPhases", func(t *testing.T) {
		t.Run("PerNodePhases", func(t *testing.T) { testListInFlightRunPhases_PerNodePhases(t, factory(t)) })
	})
	t.Run("RunStateWritesIsolated", func(t *testing.T) {
		t.Run("UpdateState", func(t *testing.T) { testRunStateWritesIsolated_UpdateState(t, factory(t)) })
		t.Run("UpdateHeartbeat", func(t *testing.T) { testRunStateWritesIsolated_UpdateHeartbeat(t, factory(t)) })
		t.Run("ClearSettlingSignalType", func(t *testing.T) { testRunStateWritesIsolated_ClearSettlingSignalType(t, factory(t)) })
		t.Run("ResetFailedTerminalSettlingSignalType", func(t *testing.T) { testRunStateWritesIsolated_ResetFailedTerminalSettlingSignalType(t, factory(t)) })
		t.Run("RemoveForNodeInTx", func(t *testing.T) { testRunStateWritesIsolated_RemoveForNodeInTx(t, factory(t)) })
		t.Run("GetParkedByNode", func(t *testing.T) { testRunStateWritesIsolated_GetParkedByNode(t, factory(t)) })
		t.Run("SetRetryNoProgressForNodeInTx", func(t *testing.T) { testRunStateWritesIsolated_SetRetryNoProgressForNodeInTx(t, factory(t)) })
		t.Run("NodeAttributesGetLatestByNode", func(t *testing.T) { testRunStateWritesIsolated_NodeAttributesGetLatestByNode(t, factory(t)) })
	})
	t.Run("RecoveryAwareDispatch", func(t *testing.T) { testRecoveryAwareDispatch(t, factory(t)) })
	// @deliberate: spec 2026-05-22-fan-out-safety-scope-first-design retired the
	// cycle-2/3 fan-out disambiguator-specific conformance tests
	// (NodesUpdateStateFanoutRunID, NodesClearLastOutcomeFanoutRunID,
	// QueueRemoveForNodeFanoutRunID, QueueEnqueueFanoutPartition,
	// QueueGetInFlightRunForNodeFanoutDisambiguator, QueueGetParkedByNodeFanoutRunID):
	// their cases became inexpressible under uq_node_runs_in_flight_per_run_scope.
	// Replacement coverage lives in RunStateWritesIsolatedByScope below.
	t.Run("NodeAttributesMergeDelta", func(t *testing.T) { testNodeAttributesMergeDelta(t, factory(t)) })
	t.Run("NodeAttributesPerRunInsertByRun", func(t *testing.T) { testNodeAttributesPerRunInsertByRun(t, factory(t)) })
	t.Run("NodeAttributesGetLatestByNode", func(t *testing.T) { testNodeAttributesGetLatestByNode(t, factory(t)) })
	t.Run("NodeAttributesCascadeDeleteWithRun", func(t *testing.T) { testNodeAttributesCascadeDeleteWithRun(t, factory(t), rawExec) })
	t.Run("NodeAttributesPerRunDenormConsistency", func(t *testing.T) { testNodeAttributesPerRunDenormConsistency(t, factory(t)) })
	t.Run("InstancesFindAnyByInstanceKey", func(t *testing.T) { testInstancesFindAnyByInstanceKey(t, factory(t)) })
	t.Run("InstancesAttributeOverridesRoundTrip", func(t *testing.T) { testInstancesAttributeOverridesRoundTrip(t, factory(t)) })
	t.Run("InstancesAttributeOverridesDefaultsEmpty", func(t *testing.T) { testInstancesAttributeOverridesDefaultsEmpty(t, factory(t)) })
	t.Run("InstancesAttributeOverridesMigrationBackfill", func(t *testing.T) { testInstancesAttributeOverridesMigrationBackfill(t, factory(t), rawExec) })
	t.Run("InstancesDeleteCascadeRunScopeTree", func(t *testing.T) {
		testInstancesDeleteCascadeRunScopeTree(t, factory(t), rawQuery)
	})
	t.Run("InstancesAttributeOverridesMatchCountsRoundTrip", func(t *testing.T) {
		testInstancesAttributeOverridesMatchCountsRoundTrip(t, factory(t))
	})
	t.Run("InstancesIncrementAttributeOverrideMatchCounts", func(t *testing.T) {
		testInstancesIncrementAttributeOverrideMatchCounts(t, factory(t))
	})
	t.Run("InstancesIncrementAttributeOverrideMatchCountsConcurrent", func(t *testing.T) {
		testInstancesIncrementAttributeOverrideMatchCountsConcurrent(t, factory(t))
	})
	t.Run("StoreLifecycleListByStore", func(t *testing.T) { testStoreLifecycleListByStore(t, factory(t)) })
	t.Run("EventsListDescending", func(t *testing.T) { testEventsListDescending(t, factory(t)) })
	t.Run("EventsListAuthPayloadFilters", func(t *testing.T) { testEventsListAuthPayloadFilters(t, factory(t)) })
	t.Run("MessagesListByFrameID", func(t *testing.T) { testMessagesListByFrameID(t, factory(t)) })
	// @deliberate: SchedulesDenseSameTimestampPagination retired by the
	// 2026-05-15 plan B10 / D7 / E16 schedule-retirement cascade.
	t.Run("WaitSet", func(t *testing.T) { testWaitSet(t, factory(t)) })
	// @decision: claimant-guard-helper — no-op coverage for invariant 4:
	// every ownership mutation must be a provable no-op for the wrong
	// supervisor; see claimant_guard.go for the operation-family map.
	t.Run("ClaimantGuard", func(t *testing.T) {
		t.Run("HandleUpdates", func(t *testing.T) { testClaimantGuardHandleUpdates(t, factory(t)) })
		t.Run("HandleCounterBumps", func(t *testing.T) { testClaimantGuardHandleCounterBumps(t, factory(t)) })
		t.Run("HandlePromote", func(t *testing.T) { testClaimantGuardHandlePromote(t, factory(t)) })
		t.Run("HandleReassignHolder", func(t *testing.T) { testClaimantGuardHandleReassignHolder(t, factory(t)) })
		t.Run("HandleDelete", func(t *testing.T) { testClaimantGuardHandleDelete(t, factory(t)) })
		t.Run("HandleDeleteIfExpired", func(t *testing.T) { testClaimantGuardHandleDeleteIfExpired(t, factory(t)) })
		t.Run("HandleExtendHeartbeat", func(t *testing.T) { testClaimantGuardHandleExtendHeartbeat(t, factory(t)) })
		t.Run("HolderRelease", func(t *testing.T) { testClaimantGuardHolderRelease(t, factory(t)) })
		t.Run("RunClaimSteal", func(t *testing.T) { testClaimantGuardRunClaimSteal(t, factory(t)) })
		t.Run("RunReleaseClaim", func(t *testing.T) { testClaimantGuardRunReleaseClaim(t, factory(t)) })
		t.Run("RunComplete", func(t *testing.T) { testClaimantGuardRunComplete(t, factory(t)) })
		t.Run("RunRemoveForNode", func(t *testing.T) { testClaimantGuardRunRemoveForNode(t, factory(t)) })
		t.Run("RunPark", func(t *testing.T) { testClaimantGuardRunPark(t, factory(t)) })
		t.Run("RunRefreshHeartbeat", func(t *testing.T) { testClaimantGuardRunRefreshHeartbeat(t, factory(t)) })
		t.Run("NodeUpdateHeartbeat", func(t *testing.T) { testClaimantGuardNodeUpdateHeartbeat(t, factory(t)) })
		t.Run("RunEmptyClaimantCarveOut", func(t *testing.T) { testClaimantGuardRunEmptyClaimantCarveOut(t, factory(t)) })
		t.Run("UnguardedMutationCarveOuts", func(t *testing.T) { testClaimantGuardUnguardedMutationCarveOuts(t, factory(t)) })
	})
	// @constraint: driver-parity expansion — runtime-consumed behaviors with
	// driver-specific SQL idioms (park/resume, frame lifecycle, retention
	// sweeps, message idempotency) must pass identically on both drivers.
	t.Run("MessageIdempotency", func(t *testing.T) {
		t.Run("InsertOrLookup", func(t *testing.T) { testMessageIdempotencyInsertOrLookup(t, factory(t)) })
		t.Run("DeleteOlderThan", func(t *testing.T) { testMessageIdempotencyDeleteOlderThan(t, factory(t)) })
	})
	t.Run("ParkResume", func(t *testing.T) {
		t.Run("SweepSelection", func(t *testing.T) { testParkResumeSweepSelection(t, factory(t)) })
		t.Run("MetadataRoundTrip", func(t *testing.T) { testParkResumeMetadataRoundTrip(t, factory(t)) })
		t.Run("ParkedDiagnostic", func(t *testing.T) { testParkResumeParkedDiagnostic(t, factory(t)) })
		t.Run("HeldFrameCount", func(t *testing.T) { testParkResumeHeldFrameCount(t, factory(t)) })
	})
	t.Run("FrameLifecycle", func(t *testing.T) {
		t.Run("SerialQueue", func(t *testing.T) { testFrameLifecycleSerialQueue(t, factory(t)) })
		t.Run("Coalesce", func(t *testing.T) { testFrameLifecycleCoalesce(t, factory(t)) })
	})
	// @constraint: FrameSettlement is the frame engine's settlement core
	// (frame-end detection, instance termination, source-node binding,
	// stuck-frame warning, orphan-dispatch reaper) and carries the most
	// driver-divergent SQL in the layer (INTERVAL arithmetic vs Go-side
	// window math), so both drivers must prove parity here.
	t.Run("FrameSettlement", func(t *testing.T) {
		t.Run("NoPendingNodes", func(t *testing.T) { testFrameSettlementNoPendingNodes(t, factory(t)) })
		t.Run("HasFailedNode", func(t *testing.T) { testFrameSettlementHasFailedNode(t, factory(t)) })
		t.Run("InstanceTermination", func(t *testing.T) { testFrameSettlementInstanceTermination(t, factory(t)) })
		t.Run("MarkSourceNodeStale", func(t *testing.T) { testFrameSettlementMarkSourceNodeStale(t, factory(t)) })
		t.Run("StuckFrames", func(t *testing.T) { testFrameSettlementStuckFrames(t, factory(t), rawExec) })
		t.Run("OrphanDispatches", func(t *testing.T) { testFrameSettlementOrphanDispatches(t, factory(t)) })
	})
	// @constraint: ClaimHandleQueries pins the runtime-consumed claim-handle
	// read/repoint surface (named-lock capacity gate, anchor walks, fan-out
	// repoint, recursive child walk, asset query) so both drivers stay in
	// parity for these runtime reads.
	t.Run("ClaimHandleQueries", func(t *testing.T) {
		t.Run("CountByNamedLock", func(t *testing.T) { testClaimHandleCountByNamedLock(t, factory(t)) })
		t.Run("AnchorsAndRepoint", func(t *testing.T) { testClaimHandleAnchorsAndRepoint(t, factory(t)) })
		t.Run("ChildWalk", func(t *testing.T) { testClaimHandleChildWalk(t, factory(t)) })
		t.Run("ListByInstanceAndState", func(t *testing.T) { testClaimHandleListByInstanceAndState(t, factory(t)) })
	})
	t.Run("RetentionSweep", func(t *testing.T) {
		t.Run("ClaimHandles", func(t *testing.T) { testRetentionClaimHandleSweep(t, factory(t)) })
		t.Run("FrameTrace", func(t *testing.T) { testRetentionFrameTracePrune(t, factory(t)) })
	})
	t.Run("LineageQueryByParentRunID", func(t *testing.T) { testLineageQueryByParentRunID(t, factory(t)) })
	t.Run("LineageCountOlderThanMatchesDelete", func(t *testing.T) { testLineageCountOlderThanMatchesDelete(t, factory(t)) })
	t.Run("APIKeys", func(t *testing.T) { TestAPIKeys(t, factory(t)) })
}
