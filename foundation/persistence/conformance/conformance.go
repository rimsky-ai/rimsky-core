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

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
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
// native style (`$N` for postgres, `?` for sqlite). Pass nil to skip
// tests that require it (none currently — both drivers must supply
// one).
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
	t.Run("QueueInTxAndDispatchNode", func(t *testing.T) { testQueueInTxAndDispatchNode(t, factory(t)) })
	t.Run("SelectCandidatesSkipsPausedInstances", func(t *testing.T) { testSelectCandidatesSkipsPausedInstances(t, factory(t)) })
	t.Run("QueueRebindRunFrameInTx", func(t *testing.T) { testQueueRebindRunFrameInTx(t, factory(t)) })
	t.Run("ClaimHandlesUpdateClaimScope", func(t *testing.T) { testClaimHandlesUpdateClaimScope(t, factory(t)) })
	// NodesMarkStaleForCascade conformance retired by spec
	// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md:
	// MarkStaleForCascade is now keyed on `runID` (pure UPDATE); allocation
	// moved to AffirmNodeRunRow. The shaped-from-nodeID + bool-return-of-
	// inserted contract that this test pinned is gone. Replacement coverage:
	// AffirmNodeRunRow conformance (testAffirmNodeRunRow) below.
	t.Run("NodesListRunningBySupervisor", func(t *testing.T) { testNodesListRunningBySupervisor(t, factory(t)) })

	// === RunScope-first conformance (Tasks 28–31, 55) ===
	// Per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
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
	t.Run("RunStateWritesIsolated", func(t *testing.T) {
		t.Run("UpdateState", func(t *testing.T) { testRunStateWritesIsolated_UpdateState(t, factory(t)) })
		t.Run("UpdateHeartbeat", func(t *testing.T) { testRunStateWritesIsolated_UpdateHeartbeat(t, factory(t)) })
		t.Run("ClearSettlingSignalType", func(t *testing.T) { testRunStateWritesIsolated_ClearSettlingSignalType(t, factory(t)) })
		t.Run("ClearSupervisorAssignment", func(t *testing.T) { testRunStateWritesIsolated_ClearSupervisorAssignment(t, factory(t)) })
		t.Run("ResetFailedTerminalSettlingSignalType", func(t *testing.T) { testRunStateWritesIsolated_ResetFailedTerminalSettlingSignalType(t, factory(t)) })
		t.Run("RemoveForNodeInTx", func(t *testing.T) { testRunStateWritesIsolated_RemoveForNodeInTx(t, factory(t)) })
		t.Run("GetParkedByNode", func(t *testing.T) { testRunStateWritesIsolated_GetParkedByNode(t, factory(t)) })
		t.Run("SetRetryNoProgressForNodeInTx", func(t *testing.T) { testRunStateWritesIsolated_SetRetryNoProgressForNodeInTx(t, factory(t)) })
		t.Run("NodeAttributesGetLatestByNode", func(t *testing.T) { testRunStateWritesIsolated_NodeAttributesGetLatestByNode(t, factory(t)) })
	})
	t.Run("RecoveryAwareDispatch", func(t *testing.T) { testRecoveryAwareDispatch(t, factory(t)) })
	// Note: cycle-2/3 fan-out disambiguator-specific conformance tests
	// (NodesUpdateStateFanoutRunID, NodesClearLastOutcomeFanoutRunID,
	// QueueRemoveForNodeFanoutRunID, QueueEnqueueFanoutPartition,
	// QueueGetInFlightRunForNodeFanoutDisambiguator, QueueGetParkedByNodeFanoutRunID)
	// were retired by spec
	// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md:
	// their cases became inexpressible under
	// uq_node_runs_in_flight_per_run_scope. The replacement coverage lives
	// in RunStateWritesIsolatedByScope below.
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
	// (SchedulesDenseSameTimestampPagination retired by the 2026-05-15
	// plan B10 / D7 / E16 schedule-retirement cascade.)
	t.Run("WaitSet", func(t *testing.T) { testWaitSet(t, factory(t)) })
	t.Run("LineageQueryByParentRunID", func(t *testing.T) { testLineageQueryByParentRunID(t, factory(t)) })
	t.Run("APIKeys", func(t *testing.T) { TestAPIKeys(t, factory(t)) })
}
