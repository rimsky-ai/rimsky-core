// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// deadlock_guard_test.go is the structural enforcement of the
// no-nil-tx contract on the persistence Store interface (option C
// from the nil-tx-deadlock audit, docs/future-work/2026-05-05-nil-tx
// -deadlock-audit.md).
//
// Two checks:
//
//  1. TestStoreMethodsRejectNilTx — every public Store method must
//     refuse a nil tx (panic). Codifies "tx is required". When a new
//     Store method is added, extend the table below.
//  2. TestNilTxFromInsideTransactionDoesNotDeadlock — documents the
//     historical SQLite hang. With MaxOpenConns=1, a nil-tx call from
//     inside an open Persist.Transaction used to deadlock (the inner
//     auto-commit path would block waiting for the only pool conn,
//     which was held by the outer tx). After option C the nil-tx code
//     path is gone, so the inner call panics and the test completes
//     in milliseconds — never timing out.
package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"

	_ "github.com/fallguy/rimsky/foundation/persistence/sqlite"
)

// openMigratedSQLite returns a 1-conn SQLite-backed driver with
// migrations applied. The driver is closed on test cleanup.
func openMigratedSQLite(t *testing.T) persistence.Driver {
	t.Helper()
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "guard.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

// expectPanic runs fn and reports whether it panicked. The fn is a
// thunk so the test table can collect the closures upfront and the
// panic recovery happens per-call.
func expectPanic(fn func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	fn()
	return false
}

// TestStoreMethodsRejectNilTx is the structural guard for option C:
// every Store method must reject a nil tx by panicking. New methods
// added to the Store interface MUST be added here.
//
// Rationale: passing a nil tx from inside an open Persist.Transaction
// silently auto-commits on a fresh connection. Under SQLite
// MaxOpenConns=1 the second connection cannot be acquired (the only
// conn is held by the outer tx) → deadlock. Outside any tx the
// silent auto-commit is also a footgun: callers can't tell that
// their reads-and-writes aren't in a single atomic unit. Option C
// removes the nil-tx code path entirely so callers must always pass
// an explicit tx.
func TestStoreMethodsRejectNilTx(t *testing.T) {
	d := openMigratedSQLite(t)
	store := d.Store()
	ctx := context.Background()
	someID := uuid.New()
	someHash := "sha256-deadbeef"

	cases := []struct {
		name string
		call func()
	}{
		// Templates
		{"Templates.Insert", func() {
			_ = store.Templates().Insert(ctx, persistence.TemplateInsertInput{}, nil)
		}},
		{"Templates.GetByHash", func() {
			_, _ = store.Templates().GetByHash(ctx, someHash, nil)
		}},
		{"Templates.List", func() {
			_, _ = store.Templates().List(ctx, persistence.TemplateListFilter{}, persistence.ListPagination{Limit: 1}, nil)
		}},
		{"Templates.UpdateState", func() {
			_ = store.Templates().UpdateState(ctx, someHash, persistence.TemplateStateRegistered, nil)
		}},
		{"Templates.DeleteByHash", func() {
			_ = store.Templates().DeleteByHash(ctx, someHash, nil)
		}},
		{"Templates.LockForUpdate", func() {
			_, _ = store.Templates().LockForUpdate(ctx, someHash, nil)
		}},
		// TemplateTags
		{"TemplateTags.Upsert", func() {
			_ = store.TemplateTags().Upsert(ctx, "t", someHash, nil)
		}},
		{"TemplateTags.Get", func() {
			_, _ = store.TemplateTags().Get(ctx, "t", nil)
		}},
		{"TemplateTags.ListByTemplate", func() {
			_, _ = store.TemplateTags().ListByTemplate(ctx, someHash, nil)
		}},
		{"TemplateTags.List", func() {
			_, _ = store.TemplateTags().List(ctx, persistence.ListPagination{Limit: 1}, nil)
		}},
		{"TemplateTags.Delete", func() {
			_, _ = store.TemplateTags().Delete(ctx, "t", nil)
		}},
		{"TemplateTags.CountByTemplate", func() {
			_, _ = store.TemplateTags().CountByTemplate(ctx, someHash, nil)
		}},
		// Instances
		{"Instances.Create", func() {
			_, _ = store.Instances().Create(ctx, persistence.InstanceCreateInput{ID: someID}, nil)
		}},
		{"Instances.Get", func() {
			_, _ = store.Instances().Get(ctx, someID, nil)
		}},
		{"Instances.GetByInstanceKey", func() {
			_, _ = store.Instances().GetByInstanceKey(ctx, someHash, "k", nil)
		}},
		{"Instances.FindAnyByInstanceKey", func() {
			_, _ = store.Instances().FindAnyByInstanceKey(ctx, "k", nil)
		}},
		{"Instances.List", func() {
			_, _ = store.Instances().List(ctx, persistence.InstanceListFilter{}, persistence.ListPagination{Limit: 1}, nil)
		}},
		{"Instances.Delete", func() {
			_ = store.Instances().Delete(ctx, someID, nil)
		}},
		{"Instances.MarkTerminated", func() {
			_ = store.Instances().MarkTerminated(ctx, someID, nil)
		}},
		{"Instances.CountActiveByTemplate", func() {
			_, _ = store.Instances().CountActiveByTemplate(ctx, someHash, nil)
		}},
		{"Instances.ListTerminatedWithLifecycleRows", func() {
			_, _ = store.Instances().ListTerminatedWithLifecycleRows(ctx, 10, nil)
		}},
		{"Instances.CountByActive", func() {
			_, _, _ = store.Instances().CountByActive(ctx, nil)
		}},
		// LifecycleIdempotency
		{"LifecycleIdempotency.Get", func() {
			_, _ = store.LifecycleIdempotency().Get(ctx, "s", persistence.LifecycleIdempotencyScopeTemplate, "x", nil)
		}},
		{"LifecycleIdempotency.Upsert", func() {
			_ = store.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{}, nil)
		}},
		{"LifecycleIdempotency.Delete", func() {
			_ = store.LifecycleIdempotency().Delete(ctx, "s", persistence.LifecycleIdempotencyScopeTemplate, "x", nil)
		}},
		{"LifecycleIdempotency.DeleteByScope", func() {
			_ = store.LifecycleIdempotency().DeleteByScope(ctx, persistence.LifecycleIdempotencyScopeTemplate, "x", nil)
		}},
		{"LifecycleIdempotency.ListByScope", func() {
			_, _ = store.LifecycleIdempotency().ListByScope(ctx, persistence.LifecycleIdempotencyScopeTemplate, "x", nil)
		}},
		{"LifecycleIdempotency.ListByStore", func() {
			_, _ = store.LifecycleIdempotency().ListByStore(ctx, "s", nil)
		}},
		// Nodes
		{"Nodes.Create", func() {
			_, _ = store.Nodes().Create(ctx, persistence.NodeCreateInput{}, nil)
		}},
		{"Nodes.Get", func() {
			_, _ = store.Nodes().Get(ctx, someID, nil)
		}},
		{"Nodes.ListByInstance", func() {
			_, _ = store.Nodes().ListByInstance(ctx, someID, nil)
		}},
		{"Nodes.ListByInstancePaged", func() {
			_, _ = store.Nodes().ListByInstancePaged(ctx, someID, persistence.ListPagination{Limit: 1}, nil)
		}},
		{"Nodes.ListReadyForDispatch", func() {
			_, _ = store.Nodes().ListReadyForDispatch(ctx, nil)
		}},
		{"Nodes.ListRunning", func() {
			_, _ = store.Nodes().ListRunning(ctx, nil)
		}},
		{"Nodes.ListDependentsOf", func() {
			_, _ = store.Nodes().ListDependentsOf(ctx, someID, nil)
		}},
		{"Nodes.ListWithStaleHeartbeat", func() {
			_, _ = store.Nodes().ListWithStaleHeartbeat(ctx, time.Now(), nil)
		}},
		{"Nodes.ListPureCascadeReady", func() {
			_, _ = store.Nodes().ListPureCascadeReady(ctx, nil)
		}},
		{"Nodes.CountByState", func() {
			_, _ = store.Nodes().CountByState(ctx, nil)
		}},
		{"Nodes.UpdateState", func() {
			_ = store.Nodes().UpdateState(ctx, someID, shared.NodeStateFresh, cascade.ReasonOperatorReset, "", nil)
		}},
		{"Nodes.UpdateHeartbeat", func() {
			_ = store.Nodes().UpdateHeartbeat(ctx, someID, time.Now(), "sup", nil)
		}},
		{"Nodes.SetFrameID", func() {
			_ = store.Nodes().SetFrameID(ctx, someID, nil, nil)
		}},
		{"Nodes.ClearSupervisorAssignment", func() {
			_ = store.Nodes().ClearSupervisorAssignment(ctx, someID, nil)
		}},
		{"Nodes.DeleteByInstance", func() {
			_ = store.Nodes().DeleteByInstance(ctx, someID, nil)
		}},
		{"Nodes.MarkStaleForCascade", func() {
			_ = store.Nodes().MarkStaleForCascade(ctx, someID, someID, nil)
		}},
		// LockHolders
		{"LockHolders.Insert", func() {
			_ = store.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{}, nil)
		}},
		{"LockHolders.UpdateAddress", func() {
			_ = store.LockHolders().UpdateAddress(ctx, someID, "sup", nil, nil)
		}},
		{"LockHolders.Get", func() {
			_, _ = store.LockHolders().Get(ctx, someID, nil)
		}},
		{"LockHolders.ListByHolderNode", func() {
			_, _ = store.LockHolders().ListByHolderNode(ctx, someID, nil)
		}},
		{"LockHolders.ListBySupervisor", func() {
			_, _ = store.LockHolders().ListBySupervisor(ctx, "sup", nil)
		}},
		{"LockHolders.ExtendHeartbeat", func() {
			_ = store.LockHolders().ExtendHeartbeat(ctx, "sup", time.Now(), nil)
		}},
		{"LockHolders.ListExpired", func() {
			_, _ = store.LockHolders().ListExpired(ctx, nil)
		}},
		{"LockHolders.Delete", func() {
			_ = store.LockHolders().Delete(ctx, someID, "sup", nil)
		}},
		{"LockHolders.CountByNamedLock", func() {
			_, _ = store.LockHolders().CountByNamedLock(ctx, "n", nil)
		}},
		{"LockHolders.ListByStoreScope", func() {
			_, _ = store.LockHolders().ListByStoreScope(ctx, "s", nil)
		}},
		{"LockHolders.DeleteIfExpired", func() {
			_, _ = store.LockHolders().DeleteIfExpired(ctx, someID, "sup", nil)
		}},
		{"LockHolders.LockForUpdate", func() {
			_, _ = store.LockHolders().LockForUpdate(ctx, someID, nil)
		}},
		{"LockHolders.UpdateScope", func() {
			_ = store.LockHolders().UpdateScope(ctx, someID, "sup", nil, nil)
		}},
		{"LockHolders.UpdateRealizedWriteSemantics", func() {
			_ = store.LockHolders().UpdateRealizedWriteSemantics(ctx, someID, "sup", "sync", nil)
		}},
		{"LockHolders.ListForObservability", func() {
			_, _ = store.LockHolders().ListForObservability(ctx, persistence.LockHolderListFilter{}, persistence.ListPagination{Limit: 1}, nil)
		}},
		{"LockHolders.GetByFrameAndNode", func() {
			_, _ = store.LockHolders().GetByFrameAndNode(ctx, someID, someID, nil)
		}},
		// NodeAttributes
		{"NodeAttributes.Get", func() {
			_, _ = store.NodeAttributes().Get(ctx, someID, nil)
		}},
		{"NodeAttributes.Upsert", func() {
			_ = store.NodeAttributes().Upsert(ctx, someID, 0, nil, nil)
		}},
		{"NodeAttributes.MergeDelta", func() {
			_ = store.NodeAttributes().MergeDelta(ctx, someID, nil, nil)
		}},
		// ClaimHolders
		{"ClaimHolders.Insert", func() {
			_ = store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{}, nil)
		}},
		{"ClaimHolders.Get", func() {
			_, _ = store.ClaimHolders().Get(ctx, someID, nil)
		}},
		{"ClaimHolders.ListByLockHolderID", func() {
			_, _ = store.ClaimHolders().ListByLockHolderID(ctx, someID, nil)
		}},
		{"ClaimHolders.ListByHolderNode", func() {
			_, _ = store.ClaimHolders().ListByHolderNode(ctx, someID, nil)
		}},
		{"ClaimHolders.ListActiveByLockHolderID", func() {
			_, _ = store.ClaimHolders().ListActiveByLockHolderID(ctx, someID, nil)
		}},
		{"ClaimHolders.Complete", func() {
			_ = store.ClaimHolders().Complete(ctx, someID, persistence.ClaimHolderStateCompleted, nil)
		}},
		{"ClaimHolders.CompleteByLockHolderAndNode", func() {
			_ = store.ClaimHolders().CompleteByLockHolderAndNode(ctx, someID, someID, persistence.ClaimHolderStateCompleted, nil)
		}},
		// Events
		{"Events.Append", func() {
			_ = store.Events().Append(ctx, persistence.EventAppendInput{}, nil)
		}},
		{"Events.List", func() {
			_, _ = store.Events().List(ctx, persistence.EventListFilter{}, persistence.ListPagination{Limit: 1}, nil)
		}},
		{"Events.LastTerminalByNodes", func() {
			_, _ = store.Events().LastTerminalByNodes(ctx, []shared.UUID{someID}, nil)
		}},
		// Schedules
		{"Schedules.Register", func() {
			_ = store.Schedules().Register(ctx, persistence.ScheduleRegisterInput{}, nil)
		}},
		{"Schedules.DueBefore", func() {
			_, _ = store.Schedules().DueBefore(ctx, time.Now(), nil)
		}},
		{"Schedules.RecordFired", func() {
			_ = store.Schedules().RecordFired(ctx, someID, time.Now(), time.Now(), nil)
		}},
		{"Schedules.ListAll", func() {
			_, _ = store.Schedules().ListAll(ctx, nil)
		}},
		{"Schedules.ForceFire", func() {
			_ = store.Schedules().ForceFire(ctx, someID, nil)
		}},
		{"Schedules.ListForObservability", func() {
			_, _ = store.Schedules().ListForObservability(ctx, persistence.ScheduleListFilter{}, persistence.ListPagination{Limit: 1}, nil)
		}},
		// Supervisors
		{"Supervisors.Register", func() {
			_ = store.Supervisors().Register(ctx, persistence.SupervisorRegisterInput{}, nil)
		}},
		{"Supervisors.Heartbeat", func() {
			_ = store.Supervisors().Heartbeat(ctx, "sup", 0, nil)
		}},
		{"Supervisors.Get", func() {
			_, _ = store.Supervisors().Get(ctx, "sup", nil)
		}},
		{"Supervisors.List", func() {
			_, _ = store.Supervisors().List(ctx, nil)
		}},
		{"Supervisors.ListStale", func() {
			_, _ = store.Supervisors().ListStale(ctx, time.Now(), nil)
		}},
		{"Supervisors.Unregister", func() {
			_ = store.Supervisors().Unregister(ctx, "sup", nil)
		}},
		// Frames
		{"Frames.ListRunningFramesNoPendingNodes", func() {
			_, _ = store.Frames().ListRunningFramesNoPendingNodes(ctx, nil)
		}},
		{"Frames.HasFailedNode", func() {
			_, _ = store.Frames().HasFailedNode(ctx, someID, someID, nil)
		}},
		{"Frames.MarkRunningFrameTerminal", func() {
			_, _ = store.Frames().MarkRunningFrameTerminal(ctx, someID, persistence.FrameStateCompleted, nil)
		}},
		{"Frames.MarkInstanceTerminatedIfDone", func() {
			_ = store.Frames().MarkInstanceTerminatedIfDone(ctx, someID, nil)
		}},
		{"Frames.ListQueuedFramesReadyToStart", func() {
			_, _ = store.Frames().ListQueuedFramesReadyToStart(ctx, nil)
		}},
		{"Frames.PromoteQueuedFrameToRunning", func() {
			_, _ = store.Frames().PromoteQueuedFrameToRunning(ctx, someID, nil)
		}},
		{"Frames.MarkSourceNodeStale", func() {
			_, _ = store.Frames().MarkSourceNodeStale(ctx, someID, someID, someID, nil)
		}},
		{"Frames.ListStuckRunningFrames", func() {
			_, _ = store.Frames().ListStuckRunningFrames(ctx, nil)
		}},
		{"Frames.ListOrphanFrameDispatches", func() {
			_, _ = store.Frames().ListOrphanFrameDispatches(ctx, nil)
		}},
		{"Frames.LookupFrameMode", func() {
			_, _, _ = store.Frames().LookupFrameMode(ctx, someID, nil)
		}},
		{"Frames.EnqueueSerialFrame", func() {
			_, _ = store.Frames().EnqueueSerialFrame(ctx, someID, someID, 1000, nil)
		}},
		{"Frames.EnqueueCoalesceFrame", func() {
			_, _ = store.Frames().EnqueueCoalesceFrame(ctx, someID, someID, 1000, nil)
		}},
		{"Frames.ListForObservability", func() {
			_, _ = store.Frames().ListForObservability(ctx, persistence.FrameListFilter{}, persistence.ListPagination{Limit: 1}, nil)
		}},
		{"Frames.GetForObservability", func() {
			_, _ = store.Frames().GetForObservability(ctx, someID, nil)
		}},
	}

	var missing []string
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !expectPanic(tc.call) {
				missing = append(missing, tc.name)
				t.Errorf("expected panic on nil tx, got none")
			}
		})
	}
	if len(missing) > 0 {
		t.Logf("methods accepting nil tx (option C violation): %s", strings.Join(missing, ", "))
	}
}

// TestNilTxFromInsideTransactionDoesNotDeadlock pins the bug shape
// from docs/future-work/2026-05-05-nil-tx-deadlock-audit.md. Before
// option C this test would deadlock (nil-tx auto-commit waiting for
// the only pool conn that the outer tx is holding); the test deadline
// catches it as a timeout. After option C the inner call panics,
// rolling back the outer tx cleanly in milliseconds.
//
// The shape under test: an outer Persist.Transaction whose callback
// calls a Store method with tx == nil. This is exactly the
// runner_locks.go bug pattern.
func TestNilTxFromInsideTransactionDoesNotDeadlock(t *testing.T) {
	d := openMigratedSQLite(t)
	store := d.Store()

	// 2 s is generous: the panic-recovery path returns in milliseconds.
	// A real deadlock (pre-option-C) would block forever and only
	// surface as DeadlineExceeded once the deadline elapses.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		// Historical bug pattern: inner call passes nil even though
		// the outer tx is right there in scope.
		defer func() { _ = recover() }() // swallow the expected panic
		_, _ = store.Nodes().Get(ctx, uuid.New(), nil)
		return nil
	})
	elapsed := time.Since(start)

	// Acceptable outcomes:
	//   - err == nil and elapsed < 1s (panic recovered, tx committed empty)
	//   - err is the panic propagated (still no deadlock)
	// Unacceptable:
	//   - elapsed ≈ deadline (deadlock surfaced as DeadlineExceeded)
	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("nil-tx-from-inside-tx took %v (≈ deadline) — deadlock not eliminated; err=%v", elapsed, err)
	}
}
