// Package conformance is the cross-driver test suite. Both Postgres and
// SQLite drivers must pass every test here. Run via the per-driver
// wrappers in conformance_test.go.
//
// Spec: §9.1.
package conformance

import (
	"testing"

	"github.com/fallguy/rimsky/core/persistence"
)

// Suite runs every conformance check against the driver returned by
// factory. Each subtest is independent; factory is called once per
// subtest so each gets a fresh DB.
func Suite(t *testing.T, factory func(*testing.T) persistence.Driver) {
	t.Helper()
	t.Run("DispatchClaimRelease", func(t *testing.T) { testDispatchClaimRelease(t, factory(t)) })
	t.Run("VerifyBeforeRunRead", func(t *testing.T) { testVerifyBeforeRunRead(t, factory(t)) })
	t.Run("MigrationIdempotency", func(t *testing.T) { testMigrationIdempotency(t, factory(t)) })
	t.Run("CoordinatorSchedulerTick", func(t *testing.T) { testCoordinatorSchedulerTick(t, factory(t)) })
	t.Run("ForeignKeyCascade", func(t *testing.T) { testForeignKeyCascade(t, factory(t)) })
	t.Run("RegionByteEquality", func(t *testing.T) { testRegionByteEquality(t, factory(t)) })
	t.Run("OrphanCutoffTime", func(t *testing.T) { testOrphanCutoffTime(t, factory(t)) })
	t.Run("TxAtomicity", func(t *testing.T) { testTxAtomicity(t, factory(t)) })
	t.Run("AcquisitionTxAtomicity", func(t *testing.T) { testAcquisitionTxAtomicity(t, factory(t)) })
	t.Run("HeldClaimAutoTerminalSerialization", func(t *testing.T) { testHeldClaimAutoTerminalSerialization(t, factory(t)) })
	t.Run("SortOrderCoordination", func(t *testing.T) { testSortOrderCoordination(t, factory(t)) })
	t.Run("CoalesceFrameNilTx", func(t *testing.T) { testCoalesceFrameNilTx(t, factory(t)) })
	t.Run("QueueInTxAndDispatchNode", func(t *testing.T) { testQueueInTxAndDispatchNode(t, factory(t)) })
	t.Run("LockHoldersUpdateRegion", func(t *testing.T) { testLockHoldersUpdateRegion(t, factory(t)) })
	t.Run("NodesMarkStaleForCascade", func(t *testing.T) { testNodesMarkStaleForCascade(t, factory(t)) })
	t.Run("NodeAttributesMergeDelta", func(t *testing.T) { testNodeAttributesMergeDelta(t, factory(t)) })
	t.Run("InstancesFindAnyByInstanceKey", func(t *testing.T) { testInstancesFindAnyByInstanceKey(t, factory(t)) })
	t.Run("StoreLifecycleListByStore", func(t *testing.T) { testStoreLifecycleListByStore(t, factory(t)) })
	t.Run("EventsListDescending", func(t *testing.T) { testEventsListDescending(t, factory(t)) })
	t.Run("SchedulesDenseSameTimestampPagination", func(t *testing.T) { testSchedulesDenseSameTimestampPagination(t, factory(t)) })
}
