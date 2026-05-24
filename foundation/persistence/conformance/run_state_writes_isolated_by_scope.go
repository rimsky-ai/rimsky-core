// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run_state_writes_isolated_by_scope.go — RunStateWritesIsolatedByScope
// conformance area.
//
// Replacement coverage for the cycle-2/3 fan-out disambiguator
// conformance tests retired by spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// (Task 32). The invariant under test: for each per-run-keyed state
// write, mutating the row in RunScope A must leave the row in RunScope
// B unchanged, when both scopes share a node_id.
//
// Methods covered:
//   - Nodes().UpdateState
//   - Nodes().UpdateHeartbeat
//   - Nodes().ClearSettlingSignalType
//   - Nodes().ClearSupervisorAssignment
//   - Nodes().ResetFailedTerminalSettlingSignalType
//   - Queue().RemoveForNodeInTx
//   - Queue().GetParkedByNode
//   - Queue().SetRetryNoProgressForNodeInTx
//   - NodeAttributes().GetLatestByNode
//
// @concept: run-scope
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// twoScopeFixture builds two RunScopes (A=main, B=fanout_partition)
// sharing the same node_id, each with an in-flight pending run row.
// Returns the (scopeA, scopeB, runA, runB) tuple.
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

	// Seed a parent run (needed because scope B is a fanout_partition
	// — it requires non-nil parent_run_id).
	parentRun := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	scopeB := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               scopeB,
			ParentRunScopeID: &scopeA,
			ParentRunID:      &parentRun,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "scope-b",
		})
	}); err != nil {
		t.Fatalf("Create scope B: %v", err)
	}

	// Affirm one in-flight row in each scope for fix.NodeID. After the
	// affirm, GetInFlightRunForNode returns the run id in each scope.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, scopeA, fix.FrameID, tx); err != nil {
			return err
		}
		return store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, scopeB, fix.FrameID, tx)
	}); err != nil {
		t.Fatalf("Affirm A and B: %v", err)
	}

	var runA, runB shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		idA, foundA, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, scopeA)
		if err != nil {
			return err
		}
		if !foundA {
			t.Fatalf("seedTwoScopeRuns: scope A in-flight not found")
		}
		runA = idA
		idB, foundB, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, scopeB)
		if err != nil {
			return err
		}
		if !foundB {
			t.Fatalf("seedTwoScopeRuns: scope B in-flight not found")
		}
		runB = idB
		return nil
	}); err != nil {
		t.Fatalf("Resolve run ids: %v", err)
	}
	if runA == runB {
		t.Fatalf("seedTwoScopeRuns: scope A and B resolved to the same run id %v", runA)
	}

	return twoScopeFixture{fix: fix, scopeA: scopeA, scopeB: scopeB, runA: runA, runB: runB}
}

// readRunState reads the (state, settling_signal_type, phase, claimed_by)
// tuple for a given run id via the RunTreeTable + Queue.GetByID. Used
// by the per-method isolation assertions.
type runRowSnapshot struct {
	State              cascade.NodeState
	SettlingSignalType string
	ClaimedBy          string
}

func snapshotRun(ctx context.Context, t *testing.T, d persistence.Database, runID shared.UUID) runRowSnapshot {
	t.Helper()
	store := d.Tables()
	q := d.Queue()
	var out runRowSnapshot
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.RunTree().GetByID(ctx, tx, runID)
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
		t.Fatalf("snapshotRun.RunTree: %v", err)
	}
	row, err := q.GetByID(ctx, runID)
	if err != nil {
		t.Fatalf("snapshotRun.Queue.GetByID: %v", err)
	}
	if row != nil && row.ClaimedBy != nil {
		out.ClaimedBy = *row.ClaimedBy
	}
	return out
}

// testRunStateWritesIsolated_UpdateState: write running on scope A;
// assert scope B's run unchanged.
func testRunStateWritesIsolated_UpdateState(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, f.fix.NodeID, f.scopeA,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	}); err != nil {
		t.Fatalf("UpdateState(A): %v", err)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if before.State != after.State {
		t.Fatalf("UpdateState leaked across scope: B.State before=%q, after=%q", before.State, after.State)
	}
}

// testRunStateWritesIsolated_UpdateHeartbeat.
func testRunStateWritesIsolated_UpdateHeartbeat(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateHeartbeat(ctx, f.fix.NodeID, f.scopeA, time.Now(), "sup-A", tx)
	}); err != nil {
		t.Fatalf("UpdateHeartbeat(A): %v", err)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if before.ClaimedBy != after.ClaimedBy {
		t.Fatalf("UpdateHeartbeat leaked supervisor across scope: B before=%q, after=%q",
			before.ClaimedBy, after.ClaimedBy)
	}
}

// testRunStateWritesIsolated_ClearSettlingSignalType: first seed both
// runs with a settling_signal_type, then clear scope A's; assert scope
// B's untouched.
func testRunStateWritesIsolated_ClearSettlingSignalType(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	// Seed both runs with a known settling_signal_type via
	// RunTree.UpdateStateAndOutcome.
	successSig := "terminal/success"
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.RunTree().UpdateStateAndOutcome(ctx, tx, f.runA, cascade.NodeStateFresh, &successSig); err != nil {
			return err
		}
		return store.RunTree().UpdateStateAndOutcome(ctx, tx, f.runB, cascade.NodeStateFresh, &successSig)
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

// testRunStateWritesIsolated_ClearSupervisorAssignment: claim scope B's
// run; clear scope A's supervisor assignment; assert B's claimed_by
// unchanged.
func testRunStateWritesIsolated_ClearSupervisorAssignment(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Nodes().UpdateHeartbeat(ctx, f.fix.NodeID, f.scopeA, time.Now(), "sup-A", tx); err != nil {
			return err
		}
		return store.Nodes().UpdateHeartbeat(ctx, f.fix.NodeID, f.scopeB, time.Now(), "sup-B", tx)
	}); err != nil {
		t.Fatalf("seed supervisors: %v", err)
	}
	before := snapshotRun(ctx, t, d, f.runB)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().ClearSupervisorAssignment(ctx, f.fix.NodeID, f.scopeA, tx)
	}); err != nil {
		t.Fatalf("ClearSupervisorAssignment(A): %v", err)
	}
	after := snapshotRun(ctx, t, d, f.runB)
	if before.ClaimedBy != after.ClaimedBy {
		t.Fatalf("ClearSupervisorAssignment leaked across scope: B before=%q after=%q",
			before.ClaimedBy, after.ClaimedBy)
	}
}

// testRunStateWritesIsolated_ResetFailedTerminalSettlingSignalType.
func testRunStateWritesIsolated_ResetFailedTerminalSettlingSignalType(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	// Seed both runs with a failed settling signal type-path.
	failedSig := "terminal/error/aggregate/strict_failed"
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.RunTree().UpdateStateAndOutcome(ctx, tx, f.runA, cascade.NodeStateFailed, &failedSig); err != nil {
			return err
		}
		return store.RunTree().UpdateStateAndOutcome(ctx, tx, f.runB, cascade.NodeStateFailed, &failedSig)
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

// testRunStateWritesIsolated_RemoveForNodeInTx: claim both runs to make
// them eligible for RemoveForNodeInTx (which is claimant-guarded);
// remove scope A's; assert scope B's still in-flight.
func testRunStateWritesIsolated_RemoveForNodeInTx(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Nodes().UpdateHeartbeat(ctx, f.fix.NodeID, f.scopeA, time.Now(), "sup-A", tx); err != nil {
			return err
		}
		return store.Nodes().UpdateHeartbeat(ctx, f.fix.NodeID, f.scopeB, time.Now(), "sup-B", tx)
	}); err != nil {
		t.Fatalf("seed supervisors: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, f.fix.NodeID, f.scopeA, "sup-A", tx)
	}); err != nil {
		t.Fatalf("RemoveForNodeInTx(A): %v", err)
	}

	// Scope B's in-flight lookup must still resolve runB.
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

// testRunStateWritesIsolated_GetParkedByNode: park scope B's run only;
// query for scope A's parked row — must return nil; query scope B's —
// must return the parked row.
func testRunStateWritesIsolated_GetParkedByNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	// Park scope B's row. ParkActiveInTx requires the row to be
	// phase='active' with claimed_by=expected; seed it via ClaimDispatchRow.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, f.runB, "sup-B")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("seed park B: ClaimDispatchRow returned !ok for runB")
		}
		return q.ParkActiveInTx(ctx, tx, persistence.ParkActiveInput{
			DispatchID:        f.runB,
			ExpectedClaimedBy: "sup-B",
			ParkedAt:          time.Now(),
			Reason:            "snooze",
			SessionToken:      "tok",
			PayloadInline:     []byte(`{}`),
		})
	}); err != nil {
		t.Fatalf("seed park B: %v", err)
	}
	_ = cascade.NodeStateRunning

	parkedA, err := q.GetParkedByNode(ctx, f.fix.NodeID, f.scopeA)
	if err != nil {
		t.Fatalf("GetParkedByNode(A): %v", err)
	}
	if parkedA != nil {
		t.Fatalf("GetParkedByNode(A) returned a row for un-parked scope A: %+v", parkedA)
	}
	parkedB, err := q.GetParkedByNode(ctx, f.fix.NodeID, f.scopeB)
	if err != nil {
		t.Fatalf("GetParkedByNode(B): %v", err)
	}
	if parkedB == nil {
		t.Fatalf("GetParkedByNode(B): nil; want parked row")
	}
}

// testRunStateWritesIsolated_SetRetryNoProgressForNodeInTx.
func testRunStateWritesIsolated_SetRetryNoProgressForNodeInTx(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.SetRetryNoProgressForNodeInTx(ctx, tx, f.fix.NodeID, f.scopeA, 5)
	}); err != nil {
		t.Fatalf("SetRetryNoProgress(A): %v", err)
	}

	// Read B's retry counter via GetRetryNoProgress (keyed by dispatch
	// id). It must still be 0.
	countB, _, err := q.GetRetryNoProgress(ctx, f.runB)
	if err != nil {
		t.Fatalf("GetRetryNoProgress(B): %v", err)
	}
	if countB != 0 {
		t.Fatalf("SetRetryNoProgress leaked across scope: B counter=%d, want 0", countB)
	}
}

// testRunStateWritesIsolated_NodeAttributesGetLatestByNode: insert
// per-run attribute rows for both runA and runB; assert
// GetLatestByNode keyed on scopeA returns runA's row, not runB's.
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
