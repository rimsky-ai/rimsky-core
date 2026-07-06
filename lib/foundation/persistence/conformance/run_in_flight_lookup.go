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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testInFlightLookup_SingleRowPerScopePerNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return func() error {
			_, err := store.Nodes().CreateCascadePending(ctx, tx, fix.NodeID, fix.MainRunScopeID, fix.FrameID)
			return err
		}()
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

func testInFlightLookup_NoFalsePositiveAcrossScopes(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	parentRun := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	scopeB := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               scopeB,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentNodeRunID:  &parentRun,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "scope-b",
		})
	}); err != nil {
		t.Fatalf("Create scope B: %v", err)
	}

	scopeA := fix.MainRunScopeID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().CreateCascadePending(ctx, tx, fix.NodeID, scopeA, fix.FrameID)
		return err
	}); err != nil {
		t.Fatalf("Affirm A: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().CreateCascadePending(ctx, tx, fix.NodeID, scopeB, fix.FrameID)
		return err
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

	_ = time.Time{}
}
