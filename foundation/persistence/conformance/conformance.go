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
// case for instances.userdata_overrides). The helper is responsible
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
	t.Run("ClaimHandlesUpdateScope", func(t *testing.T) { testClaimHandlesUpdateScope(t, factory(t)) })
	t.Run("NodesMarkStaleForCascade", func(t *testing.T) { testNodesMarkStaleForCascade(t, factory(t)) })
	t.Run("NodesListRunningBySupervisor", func(t *testing.T) { testNodesListRunningBySupervisor(t, factory(t)) })
	t.Run("NodeAttributesMergeDelta", func(t *testing.T) { testNodeAttributesMergeDelta(t, factory(t)) })
	t.Run("InstancesFindAnyByInstanceKey", func(t *testing.T) { testInstancesFindAnyByInstanceKey(t, factory(t)) })
	t.Run("InstancesUserdataOverridesRoundTrip", func(t *testing.T) { testInstancesUserdataOverridesRoundTrip(t, factory(t)) })
	t.Run("InstancesUserdataOverridesDefaultsEmpty", func(t *testing.T) { testInstancesUserdataOverridesDefaultsEmpty(t, factory(t)) })
	t.Run("InstancesUserdataOverridesMigrationBackfill", func(t *testing.T) { testInstancesUserdataOverridesMigrationBackfill(t, factory(t), rawExec) })
	t.Run("StoreLifecycleListByStore", func(t *testing.T) { testStoreLifecycleListByStore(t, factory(t)) })
	t.Run("EventsListDescending", func(t *testing.T) { testEventsListDescending(t, factory(t)) })
	t.Run("SchedulesDenseSameTimestampPagination", func(t *testing.T) { testSchedulesDenseSameTimestampPagination(t, factory(t)) })
}
