// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: run-scope
package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testInFlightLookupSingleRowPerScopePerNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
		return err
	}); err != nil {
		t.Fatalf("Affirm: %v", err)
	}

	var runID shared.UUID
	var ok bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, found, err := q.GetInFlightRunForNode(ctx, fix.NodeID, fix.MainRunScopeID, tx)
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

func testInFlightLookupNoFalsePositiveAcrossScopes(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	parentRun := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	scopeB := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:               scopeB,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentNodeRunID:  &parentRun,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "scope-b",
		}, tx)
	}); err != nil {
		t.Fatalf("Create scope B: %v", err)
	}

	scopeA := fix.MainRunScopeID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, scopeA, fix.FrameID, tx)
		return err
	}); err != nil {
		t.Fatalf("Affirm A: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, scopeB, fix.FrameID, tx)
		return err
	}); err != nil {
		t.Fatalf("Affirm B: %v", err)
	}

	var idA shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, found, err := q.GetInFlightRunForNode(ctx, fix.NodeID, scopeA, tx)
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
		id, found, err := q.GetInFlightRunForNode(ctx, fix.NodeID, scopeB, tx)
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

// @concept: wait-set
func testInFlightLookupDeterministicWithMultipleCoexistingPendings(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	var earliest shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
		earliest = id
		return err
	}); err != nil {
		t.Fatalf("CreateCascadePending(first): %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
		return err
	}); err != nil {
		t.Fatalf("CreateCascadePending(second): %v", err)
	}

	for i := 0; i < 5; i++ {
		var id shared.UUID
		var found bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			id, found, err = q.GetInFlightRunForNode(ctx, fix.NodeID, fix.MainRunScopeID, tx)
			return err
		}); err != nil {
			t.Fatalf("GetInFlightRunForNode: %v", err)
		}
		if !found {
			t.Fatalf("GetInFlightRunForNode: not found with two coexisting pendings")
		}
		if id != earliest {
			t.Fatalf("GetInFlightRunForNode with two coexisting pendings returned %s, want the sequence-earliest row %s (dispatch claims in sequence order)", id, earliest)
		}
	}
}

func testInFlightLookupReturnsNoneWhenAbsent(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	missingNode := shared.UUID(uuid.New())
	var id shared.UUID
	var found bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		x, ok, err := q.GetInFlightRunForNode(ctx, missingNode, fix.MainRunScopeID, tx)
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
}
