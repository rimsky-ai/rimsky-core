// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope

// @constraint: RunInFlightLookup conformance area.
// Covers Queue.GetInFlightRunForNode under the post-2026-05-22 reshape:
// in-flight uniqueness is keyed on (node_id, run_scope_id) via the
// uq_node_runs_in_flight_per_run_scope partial-unique index. Two
// concurrent RunScopes sharing a node_id MUST NOT alias on lookup.
// Per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
//
// @concept: run-scope
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// testInFlightLookup_SingleRowPerScopePerNode: seed an in-flight row in
// the main RunScope; assert GetInFlightRunForNode resolves it.
func testInFlightLookup_SingleRowPerScopePerNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
	}); err != nil {
		t.Fatalf("Affirm: %v", err)
	}

	var runID shared.UUID
	var ok bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, found, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, fix.MainRunScopeID)
		runID = id
		ok = found
		return err
	}); err != nil {
		t.Fatalf("GetInFlightRunForNode: %v", err)
	}
	if !ok {
		t.Fatalf("GetInFlightRunForNode: not found after affirm")
	}
	if runID == (shared.UUID{}) {
		t.Fatalf("GetInFlightRunForNode: returned zero run id")
	}
}

// testInFlightLookup_NoFalsePositiveAcrossScopes: two RunScopes sharing
// the same node_id, each with an in-flight row. Each scope's lookup
// must return its own row, never the sibling's.
func testInFlightLookup_NoFalsePositiveAcrossScopes(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	// @constraint: scope B's parent_run_id must reference a real
	// rimsky_node_runs row to satisfy the FK; seed a fresh run row first.
	parentRun := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	scopeB := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               scopeB,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentRunID:      &parentRun,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "scope-b",
		})
	}); err != nil {
		t.Fatalf("Create scope B: %v", err)
	}

	scopeA := fix.MainRunScopeID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, scopeA, fix.FrameID, tx)
	}); err != nil {
		t.Fatalf("Affirm A: %v", err)
	}

	// @constraint: seed the cross-scope same-node case — fix.NodeID has
	// an in-flight row in both scopeA and scopeB. The lookups below
	// exercise the partial-unique index's (node_id, run_scope_id) key.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, scopeB, fix.FrameID, tx)
	}); err != nil {
		t.Fatalf("Affirm B: %v", err)
	}

	var idA shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, found, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, scopeA)
		if !found {
			t.Fatalf("lookup A: not found")
		}
		idA = id
		return err
	}); err != nil {
		t.Fatalf("lookup A: %v", err)
	}

	var idB shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, found, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, scopeB)
		if !found {
			t.Fatalf("lookup B: not found")
		}
		idB = id
		return err
	}); err != nil {
		t.Fatalf("lookup B: %v", err)
	}

	if idA == idB {
		t.Fatalf("Cross-scope lookup aliased: scope A and scope B returned the same run id %v", idA)
	}
}

// testInFlightLookup_ReturnsNoneWhenAbsent: empty state; lookup must
// return (zero, false, nil).
func testInFlightLookup_ReturnsNoneWhenAbsent(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	missingNode := shared.UUID(uuid.New())
	var id shared.UUID
	var found bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		x, ok, err := q.GetInFlightRunForNode(ctx, tx, missingNode, fix.MainRunScopeID)
		id = x
		found = ok
		return err
	}); err != nil {
		t.Fatalf("GetInFlightRunForNode (missing): %v", err)
	}
	if found {
		t.Fatalf("GetInFlightRunForNode (missing): found=true, want false")
	}
	if id != (shared.UUID{}) {
		t.Fatalf("GetInFlightRunForNode (missing): id=%v, want zero", id)
	}

	// @deliberate: silence unused-import for time, used only by sibling
	// conformance files in this package.
	_ = time.Time{}
}
