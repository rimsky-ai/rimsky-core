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

	"github.com/fallguy/rimsky/foundation/persistence"
)

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
func Suite(t *testing.T, factory func(*testing.T) persistence.Database, rawExec func(t *testing.T, d persistence.Database, sql string, args ...any)) {
	t.Helper()
	t.Run("DispatchClaimRelease", func(t *testing.T) { testDispatchClaimRelease(t, factory(t)) })
	t.Run("VerifyBeforeRunRead", func(t *testing.T) { testVerifyBeforeRunRead(t, factory(t)) })
	t.Run("MigrationIdempotency", func(t *testing.T) { testMigrationIdempotency(t, factory(t)) })
	t.Run("CoordinatorSchedulerTick", func(t *testing.T) { testCoordinatorSchedulerTick(t, factory(t)) })
	t.Run("ForeignKeyCascade", func(t *testing.T) { testForeignKeyCascade(t, factory(t)) })
	t.Run("ScopeByteEquality", func(t *testing.T) { testScopeByteEquality(t, factory(t)) })
	t.Run("OrphanCutoffTime", func(t *testing.T) { testOrphanCutoffTime(t, factory(t)) })
	t.Run("TxAtomicity", func(t *testing.T) { testTxAtomicity(t, factory(t)) })
	t.Run("AcquisitionTxAtomicity", func(t *testing.T) { testAcquisitionTxAtomicity(t, factory(t)) })
	t.Run("HeldClaimAutoTerminalSerialization", func(t *testing.T) { testHeldClaimAutoTerminalSerialization(t, factory(t)) })
	t.Run("SortOrderCoordination", func(t *testing.T) { testSortOrderCoordination(t, factory(t)) })
	t.Run("QueueInTxAndDispatchNode", func(t *testing.T) { testQueueInTxAndDispatchNode(t, factory(t)) })
	t.Run("QueueRebindRunFrameInTx", func(t *testing.T) { testQueueRebindRunFrameInTx(t, factory(t)) })
	t.Run("ClaimHandlesUpdateScope", func(t *testing.T) { testClaimHandlesUpdateScope(t, factory(t)) })
	t.Run("NodesMarkStaleForCascade", func(t *testing.T) { testNodesMarkStaleForCascade(t, factory(t)) })
	t.Run("NodesListRunningBySupervisor", func(t *testing.T) { testNodesListRunningBySupervisor(t, factory(t)) })
	t.Run("NodeAttributesMergeDelta", func(t *testing.T) { testNodeAttributesMergeDelta(t, factory(t)) })
	t.Run("NodeAttributesPerRunInsertByRun", func(t *testing.T) { testNodeAttributesPerRunInsertByRun(t, factory(t)) })
	t.Run("NodeAttributesGetLatestByNode", func(t *testing.T) { testNodeAttributesGetLatestByNode(t, factory(t)) })
	t.Run("NodeAttributesCascadeDeleteWithRun", func(t *testing.T) { testNodeAttributesCascadeDeleteWithRun(t, factory(t), rawExec) })
	t.Run("NodeAttributesPerRunDenormConsistency", func(t *testing.T) { testNodeAttributesPerRunDenormConsistency(t, factory(t)) })
	t.Run("InstancesFindAnyByInstanceKey", func(t *testing.T) { testInstancesFindAnyByInstanceKey(t, factory(t)) })
	t.Run("InstancesAttributeOverridesRoundTrip", func(t *testing.T) { testInstancesAttributeOverridesRoundTrip(t, factory(t)) })
	t.Run("InstancesAttributeOverridesDefaultsEmpty", func(t *testing.T) { testInstancesAttributeOverridesDefaultsEmpty(t, factory(t)) })
	t.Run("InstancesAttributeOverridesMigrationBackfill", func(t *testing.T) { testInstancesAttributeOverridesMigrationBackfill(t, factory(t), rawExec) })
	t.Run("StoreLifecycleListByStore", func(t *testing.T) { testStoreLifecycleListByStore(t, factory(t)) })
	t.Run("EventsListDescending", func(t *testing.T) { testEventsListDescending(t, factory(t)) })
	// (SchedulesDenseSameTimestampPagination retired by the 2026-05-15
	// plan B10 / D7 / E16 schedule-retirement cascade.)
	t.Run("WaitSet", func(t *testing.T) { testWaitSet(t, factory(t)) })
	t.Run("LineageQueryByParentRunID", func(t *testing.T) { testLineageQueryByParentRunID(t, factory(t)) })
	t.Run("APIKeys", func(t *testing.T) { TestAPIKeys(t, factory(t)) })
}
