// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
		return store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:               scopeB,
			ParentRunScopeID: &scopeA,
			ParentNodeRunID:  &runA,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "scope-b",
		}, tx)
	}); err != nil {
		t.Fatalf("Create scope B: %v", err)
	}

	runB := seedConformanceRunForScope(ctx, t, d, fix.NodeID, fix.FrameID, scopeB)

	var inFlightA, inFlightB shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		idA, foundA, err := q.GetInFlightRunForNode(ctx, fix.NodeID, scopeA, tx)
		if err != nil {
			return err
		}
		if !foundA {
			t.Fatalf("seedTwoScopeRuns: scope A in-flight not found")
		}
		inFlightA = idA
		idB, foundB, err := q.GetInFlightRunForNode(ctx, fix.NodeID, scopeB, tx)
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

func snapshotsEqual(a, b runRowSnapshot) bool {
	if a.State != b.State || a.SettlingSignalType != b.SettlingSignalType || a.ClaimedBy != b.ClaimedBy {
		return false
	}
	if (a.LastProgressAt == nil) != (b.LastProgressAt == nil) {
		return false
	}
	return a.LastProgressAt == nil || a.LastProgressAt.Equal(*b.LastProgressAt)
}

func isLiveDispatchState(s cascade.NodeState) bool {
	switch s {
	case cascade.NodeStatePending, cascade.NodeStateStale, cascade.NodeStateRunning,
		cascade.NodeStateHeld, cascade.NodeStateParked:
		return true
	}
	return false
}

func snapshotRun(ctx context.Context, t *testing.T, d persistence.Database, runID shared.UUID) runRowSnapshot {
	t.Helper()
	store := d.Tables()
	q := d.Queue()
	var out runRowSnapshot
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeRunTree().GetByID(ctx, runID, tx)
		if err != nil {
			return err
		}
		if r == nil {
			t.Fatalf("snapshotRun.NodeRunTree: run %s not found", runID)
		}
		out.State = r.State
		if r.SettlingSignalType != nil {
			out.SettlingSignalType = *r.SettlingSignalType
		}
		return nil
	}); err != nil {
		t.Fatalf("snapshotRun.NodeRunTree: %v", err)
	}
	row, err := q.GetByID(ctx, runID)
	if err != nil {
		t.Fatalf("snapshotRun.Queue.GetByID: %v", err)
	}
	if row == nil && isLiveDispatchState(out.State) {
		t.Fatalf("snapshotRun.Queue.GetByID: run %s in live state %q not found in the dispatch queue", runID, out.State)
	}
	if row == nil {
		return out
	}
	if row.ClaimedBy != nil {
		out.ClaimedBy = *row.ClaimedBy
	}
	if row.LastProgressAt != nil {
		t := *row.LastProgressAt
		out.LastProgressAt = &t
	}
	return out
}

func testRunStateWritesIsolated_UpdateState(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	beforeA := snapshotRun(ctx, t, d, f.runA)
	if beforeA.State == cascade.NodeStateRunning {
		t.Fatalf("UpdateState precondition failed: A.State already %q before the call", beforeA.State)
	}
	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, f.runA,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	}); err != nil {
		t.Fatalf("UpdateState(A): %v", err)
	}
	afterA := snapshotRun(ctx, t, d, f.runA)
	if afterA.State != cascade.NodeStateRunning {
		t.Fatalf("UpdateState(A) did not land: A.State=%q, want %q", afterA.State, cascade.NodeStateRunning)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if !snapshotsEqual(before, after) {
		t.Fatalf("UpdateState leaked across scope: B snapshot before=%+v, after=%+v", before, after)
	}
}

func testRunStateWritesIsolated_BumpLastProgressAt(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		okA, err := q.ClaimDispatchRow(ctx, f.runA, "sup-A", tx)
		if err != nil {
			return err
		}
		if !okA {
			t.Fatalf("seed claims: ClaimDispatchRow(A) returned ok=false")
		}
		okB, err := q.ClaimDispatchRow(ctx, f.runB, "sup-B", tx)
		if err != nil {
			return err
		}
		if !okB {
			t.Fatalf("seed claims: ClaimDispatchRow(B) returned ok=false")
		}
		return nil
	}); err != nil {
		t.Fatalf("seed claims: %v", err)
	}

	bumpedTo := time.Now().Add(1 * time.Hour)
	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, berr := q.BumpLastProgressAt(ctx, f.runA, bumpedTo, tx)
		if berr != nil {
			return berr
		}
		if !ok {
			t.Fatalf("BumpLastProgressAt(A) returned ok=false")
		}
		return nil
	}); err != nil {
		t.Fatalf("BumpLastProgressAt(A): %v", err)
	}
	afterA := snapshotRun(ctx, t, d, f.runA)
	if afterA.LastProgressAt == nil || !afterA.LastProgressAt.Equal(bumpedTo) {
		t.Fatalf("BumpLastProgressAt(A) did not land: A.LastProgressAt=%v, want %v", afterA.LastProgressAt, bumpedTo)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if !snapshotsEqual(before, after) {
		t.Fatalf("BumpLastProgressAt leaked across scope: B snapshot before=%+v, after=%+v", before, after)
	}
}

func testRunStateWritesIsolated_ResetFailedTerminalSettlingSignalType(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	failedSig := "terminal/error/aggregate/strict_failed"
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.NodeRunTree().UpdateStateAndOutcome(ctx, f.runA, cascade.NodeStateFailed, &failedSig, false, tx); err != nil {
			return err
		}
		return store.NodeRunTree().UpdateStateAndOutcome(ctx, f.runB, cascade.NodeStateFailed, &failedSig, false, tx)
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	beforeA := snapshotRun(ctx, t, d, f.runA)
	if beforeA.SettlingSignalType != failedSig {
		t.Fatalf("ResetFailedTerminalSettlingSignalType: precondition failed, A.SettlingSignalType=%q want %q",
			beforeA.SettlingSignalType, failedSig)
	}
	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().ResetFailedTerminalSettlingSignalType(ctx, f.fix.NodeID, f.scopeA, tx)
	}); err != nil {
		t.Fatalf("ResetFailedTerminalSettlingSignalType(A): %v", err)
	}
	afterA := snapshotRun(ctx, t, d, f.runA)
	if afterA.SettlingSignalType != "" {
		t.Fatalf("ResetFailedTerminalSettlingSignalType did not fire: A.SettlingSignalType=%q, want cleared",
			afterA.SettlingSignalType)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if before.SettlingSignalType != after.SettlingSignalType {
		t.Fatalf("ResetFailedTerminalSettlingSignalType leaked across scope: B before=%q after=%q",
			before.SettlingSignalType, after.SettlingSignalType)
	}
}

func testRunStateWritesIsolated_RemoveForNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		okA, err := q.ClaimDispatchRow(ctx, f.runA, "sup-A", tx)
		if err != nil {
			return err
		}
		if !okA {
			t.Fatalf("seed supervisors: ClaimDispatchRow(A) returned ok=false")
		}
		okB, err := q.ClaimDispatchRow(ctx, f.runB, "sup-B", tx)
		if err != nil {
			return err
		}
		if !okB {
			t.Fatalf("seed supervisors: ClaimDispatchRow(B) returned ok=false")
		}
		return nil
	}); err != nil {
		t.Fatalf("seed supervisors: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.RemoveForNode(ctx, f.fix.NodeID, f.scopeA, "sup-A", tx)
	}); err != nil {
		t.Fatalf("RemoveForNode(A): %v", err)
	}

	ownerA, err := q.GetClaimedBy(ctx, f.runA)
	if err != nil {
		t.Fatalf("GetClaimedBy(A): %v", err)
	}
	if ownerA.Kind != persistence.ClaimOwnershipKindUnclaimed {
		t.Fatalf("RemoveForNode(A) did not clear scope A's claim (a no-op node_id/run_scope_id filter "+
			"would silently match zero rows): ownership=%s/%s", ownerA.Kind, ownerA.SupervisorID)
	}

	var idB shared.UUID
	var foundB bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, ok, err := q.GetInFlightRunForNode(ctx, f.fix.NodeID, f.scopeB, tx)
		idB = id
		foundB = ok
		return err
	}); err != nil {
		t.Fatalf("GetInFlightRunForNode(B): %v", err)
	}
	if !foundB || idB != f.runB {
		t.Fatalf("RemoveForNode leaked: scope B in-flight lookup returned %v (found=%v), want %v", idB, foundB, f.runB)
	}
}

func testRunStateWritesIsolated_GetParkedByNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, f.runB, "sup-B", tx)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("seed park B: ClaimDispatchRow returned !ok for runB")
		}
		if _, err := q.PromoteClaimedToRunning(ctx, f.runB, "sup-B", tx); err != nil {
			return err
		}
		if err := q.ParkActive(ctx, persistence.ParkActiveInput{
			NodeRunID:         f.runB,
			ExpectedClaimedBy: "sup-B",
			ParkedAt:          time.Now(),
		}, tx); err != nil {
			return err
		}
		return d.Tables().Nodes().UpdateState(ctx, f.runB,
			cascade.NodeStateParked, cascade.ReasonHandlerPark, nil, tx)
	}); err != nil {
		t.Fatalf("seed park B: %v", err)
	}

	parkedA, err := q.GetParkedByNode(ctx, f.fix.NodeID, f.scopeA, nil)
	if err != nil {
		t.Fatalf("GetParkedByNode(A): %v", err)
	}
	if parkedA != nil {
		t.Fatalf("GetParkedByNode(A) returned a row for un-parked scope A: %+v", parkedA)
	}
	parkedB, err := q.GetParkedByNode(ctx, f.fix.NodeID, f.scopeB, nil)
	if err != nil {
		t.Fatalf("GetParkedByNode(B): %v", err)
	}
	if parkedB == nil {
		t.Fatalf("GetParkedByNode(B): nil; want parked row")
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

	var rowB *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetLatestByNode(ctx, f.fix.NodeID, f.scopeB, tx)
		rowB = r
		return err
	}); err != nil {
		t.Fatalf("GetLatestByNode(B): %v", err)
	}
	if rowB == nil {
		t.Fatalf("GetLatestByNode(B): nil")
	}
	if v, _ := rowB.Data["which"].(string); v != "B" {
		t.Fatalf("GetLatestByNode(B) leaked across scope (or returned the first-inserted row instead of "+
			"the scope-matched one): data.which=%v, want B", rowB.Data["which"])
	}
}
