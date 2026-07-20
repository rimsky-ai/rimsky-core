// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope

// @concept: run-scope
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type twoScopeFixture struct {
	fix    fixtureSet
	scopeA shared.UUID
	scopeB shared.UUID
	runA   shared.UUID
	runB   shared.UUID
}

func seedTwoScopeRuns(ctx context.Context, t *testing.T, d persistence.Database) twoScopeFixture {
	t.Helper()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	scopeA := fix.MainRunScopeID

	runA := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	scopeB := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               scopeB,
			ParentRunScopeID: &scopeA,
			ParentNodeRunID:  &runA,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "scope-b",
		})
	}); err != nil {
		t.Fatalf("Create scope B: %v", err)
	}

	runB := seedConformanceRunForScope(ctx, t, d, fix.NodeID, fix.FrameID, scopeB)

	var inFlightA, inFlightB shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		idA, foundA, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, scopeA)
		if err != nil {
			return err
		}
		if !foundA {
			t.Fatalf("seedTwoScopeRuns: scope A in-flight not found")
		}
		inFlightA = idA
		idB, foundB, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, scopeB)
		if err != nil {
			return err
		}
		if !foundB {
			t.Fatalf("seedTwoScopeRuns: scope B in-flight not found")
		}
		inFlightB = idB
		return nil
	}); err != nil {
		t.Fatalf("Resolve run ids: %v", err)
	}
	if inFlightA != runA {
		t.Fatalf("seedTwoScopeRuns: scope A in-flight %v != seed runA %v", inFlightA, runA)
	}
	if inFlightB != runB {
		t.Fatalf("seedTwoScopeRuns: scope B in-flight %v != seed runB %v", inFlightB, runB)
	}

	return twoScopeFixture{fix: fix, scopeA: scopeA, scopeB: scopeB, runA: runA, runB: runB}
}

type runRowSnapshot struct {
	State              cascade.NodeState
	SettlingSignalType string
	ClaimedBy          string
	LastProgressAt     *time.Time
}

func snapshotRun(ctx context.Context, t *testing.T, d persistence.Database, runID shared.UUID) runRowSnapshot {
	t.Helper()
	store := d.Tables()
	q := d.Queue()
	var out runRowSnapshot
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeRunTree().GetByID(ctx, tx, runID)
		if err != nil {
			return err
		}
		if r != nil {
			out.State = r.State
			if r.SettlingSignalType != nil {
				out.SettlingSignalType = *r.SettlingSignalType
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("snapshotRun.NodeRunTree: %v", err)
	}
	row, err := q.GetByID(ctx, runID)
	if err != nil {
		t.Fatalf("snapshotRun.Queue.GetByID: %v", err)
	}
	if row != nil {
		if row.ClaimedBy != nil {
			out.ClaimedBy = *row.ClaimedBy
		}
		if row.LastProgressAt != nil {
			t := *row.LastProgressAt
			out.LastProgressAt = &t
		}
	}
	return out
}

func testRunStateWritesIsolated_UpdateState(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, f.runA,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	}); err != nil {
		t.Fatalf("UpdateState(A): %v", err)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if before.State != after.State {
		t.Fatalf("UpdateState leaked across scope: B.State before=%q, after=%q", before.State, after.State)
	}
}

func testRunStateWritesIsolated_BumpLastProgressAt(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := q.ClaimDispatchRow(ctx, tx, f.runA, "sup-A"); err != nil {
			return err
		}
		_, err := q.ClaimDispatchRow(ctx, tx, f.runB, "sup-B")
		return err
	}); err != nil {
		t.Fatalf("seed claims: %v", err)
	}

	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, berr := q.BumpLastProgressAt(ctx, tx, f.runA, time.Now().Add(1*time.Hour))
		return berr
	}); err != nil {
		t.Fatalf("BumpLastProgressAt(A): %v", err)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if (before.LastProgressAt == nil) != (after.LastProgressAt == nil) ||
		(before.LastProgressAt != nil && !before.LastProgressAt.Equal(*after.LastProgressAt)) {
		t.Fatalf("BumpLastProgressAt leaked across scope: B before=%v after=%v",
			before.LastProgressAt, after.LastProgressAt)
	}
}

func testRunStateWritesIsolated_ClearSettlingSignalType(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	successSig := "terminal/success"
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.NodeRunTree().UpdateStateAndOutcome(ctx, tx, f.runA, cascade.NodeStateFresh, &successSig); err != nil {
			return err
		}
		return store.NodeRunTree().UpdateStateAndOutcome(ctx, tx, f.runB, cascade.NodeStateFresh, &successSig)
	}); err != nil {
		t.Fatalf("seed settling_signal_type: %v", err)
	}
	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().ClearSettlingSignalType(ctx, f.fix.NodeID, f.scopeA, tx)
	}); err != nil {
		t.Fatalf("ClearSettlingSignalType(A): %v", err)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if before.SettlingSignalType != after.SettlingSignalType {
		t.Fatalf("ClearSettlingSignalType leaked across scope: B.SettlingSignalType before=%q after=%q",
			before.SettlingSignalType, after.SettlingSignalType)
	}
}

func testRunStateWritesIsolated_ResetFailedTerminalSettlingSignalType(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	failedSig := "terminal/error/aggregate/strict_failed"
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.NodeRunTree().UpdateStateAndOutcome(ctx, tx, f.runA, cascade.NodeStateFailed, &failedSig); err != nil {
			return err
		}
		return store.NodeRunTree().UpdateStateAndOutcome(ctx, tx, f.runB, cascade.NodeStateFailed, &failedSig)
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().ResetFailedTerminalSettlingSignalType(ctx, f.fix.NodeID, f.scopeA, tx)
	}); err != nil {
		t.Fatalf("ResetFailedTerminalSettlingSignalType(A): %v", err)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if before.SettlingSignalType != after.SettlingSignalType {
		t.Fatalf("ResetFailedTerminalSettlingSignalType leaked across scope: B before=%q after=%q",
			before.SettlingSignalType, after.SettlingSignalType)
	}
}

func testRunStateWritesIsolated_RemoveForNodeInTx(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := q.ClaimDispatchRow(ctx, tx, f.runA, "sup-A"); err != nil {
			return err
		}
		_, err := q.ClaimDispatchRow(ctx, tx, f.runB, "sup-B")
		return err
	}); err != nil {
		t.Fatalf("seed supervisors: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, f.fix.NodeID, f.scopeA, "sup-A", tx)
	}); err != nil {
		t.Fatalf("RemoveForNodeInTx(A): %v", err)
	}

	var idB shared.UUID
	var foundB bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, ok, err := q.GetInFlightRunForNode(ctx, tx, f.fix.NodeID, f.scopeB)
		idB = id
		foundB = ok
		return err
	}); err != nil {
		t.Fatalf("GetInFlightRunForNode(B): %v", err)
	}
	if !foundB || idB != f.runB {
		t.Fatalf("RemoveForNodeInTx leaked: scope B in-flight lookup returned %v (found=%v), want %v", idB, foundB, f.runB)
	}
}

func testRunStateWritesIsolated_GetParkedByNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, f.runB, "sup-B")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("seed park B: ClaimDispatchRow returned !ok for runB")
		}
		if _, err := q.PromoteClaimedToRunning(ctx, tx, f.runB, "sup-B"); err != nil {
			return err
		}
		if err := q.ParkActiveInTx(ctx, tx, persistence.ParkActiveInput{
			NodeRunID:         f.runB,
			ExpectedClaimedBy: "sup-B",
			ParkedAt:          time.Now(),
		}); err != nil {
			return err
		}
		return d.Tables().Nodes().UpdateState(ctx, f.runB,
			cascade.NodeStateParked, cascade.ReasonHandlerPark, nil, tx)
	}); err != nil {
		t.Fatalf("seed park B: %v", err)
	}

	parkedA, err := q.GetParkedByNode(ctx, nil, f.fix.NodeID, f.scopeA)
	if err != nil {
		t.Fatalf("GetParkedByNode(A): %v", err)
	}
	if parkedA != nil {
		t.Fatalf("GetParkedByNode(A) returned a row for un-parked scope A: %+v", parkedA)
	}
	parkedB, err := q.GetParkedByNode(ctx, nil, f.fix.NodeID, f.scopeB)
	if err != nil {
		t.Fatalf("GetParkedByNode(B): %v", err)
	}
	if parkedB == nil {
		t.Fatalf("GetParkedByNode(B): nil; want parked row")
	}
}

func testRunStateWritesIsolated_SetRetryNoProgressForRunInTx(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.SetRetryNoProgressForRunInTx(ctx, tx, f.runA, 5)
	}); err != nil {
		t.Fatalf("SetRetryNoProgress(A): %v", err)
	}

	countA, _, err := q.GetRetryNoProgress(ctx, f.runA)
	if err != nil {
		t.Fatalf("GetRetryNoProgress(A): %v", err)
	}
	if countA != 5 {
		t.Fatalf("SetRetryNoProgress(A) did not persist: A counter=%d, want 5", countA)
	}

	countB, _, err := q.GetRetryNoProgress(ctx, f.runB)
	if err != nil {
		t.Fatalf("GetRetryNoProgress(B): %v", err)
	}
	if countB != 0 {
		t.Fatalf("SetRetryNoProgress leaked across scope: B counter=%d, want 0", countB)
	}
}

func testRunStateWritesIsolated_UpdateStateWritesNoAuditEvent(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedFixtureSet(ctx, t, d)
	runID := seedConformanceRunForNode(ctx, t, d, f.NodeID, f.FrameID)
	store := d.Tables()

	countEvents := func() int {
		t.Helper()
		var n int
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			res, err := store.Events().List(ctx, persistence.EventListFilter{InstanceID: &f.InstanceID},
				persistence.ListPagination{Limit: 1000}, tx)
			if err != nil {
				return err
			}
			n = len(res.Events)
			return nil
		}); err != nil {
			t.Fatalf("Events().List: %v", err)
		}
		return n
	}

	before := countEvents()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, runID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	}); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	after := countEvents()
	if after != before {
		t.Fatalf("UpdateState wrote %d audit event row(s); the per-node state-update path must write NO "+
			"rimsky_events row (TransitionReason is validation-only, never an audit-write role): before=%d after=%d",
			after-before, before, after)
	}
}

func testRunStateWritesIsolated_NodeAttributesGetLatestByNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.NodeAttributes().Upsert(ctx, f.runA, f.fix.NodeID, map[string]any{"which": "A"}, tx); err != nil {
			return err
		}
		return store.NodeAttributes().Upsert(ctx, f.runB, f.fix.NodeID, map[string]any{"which": "B"}, tx)
	}); err != nil {
		t.Fatalf("seed attributes: %v", err)
	}

	var rowA *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetLatestByNode(ctx, f.fix.NodeID, f.scopeA, tx)
		rowA = r
		return err
	}); err != nil {
		t.Fatalf("GetLatestByNode(A): %v", err)
	}
	if rowA == nil {
		t.Fatalf("GetLatestByNode(A): nil")
	}
	if v, _ := rowA.Data["which"].(string); v != "A" {
		t.Fatalf("GetLatestByNode(A) leaked across scope: data.which=%v, want A", rowA.Data["which"])
	}
}
