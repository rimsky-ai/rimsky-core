// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope

// @concept: run-scope
package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testAffirmNodeRunRow_InsertsWhenNoInFlight(t *testing.T, d persistence.Database) {
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
		t.Fatalf("Affirm did not insert a row; GetInFlightRunForNode returned not-found")
	}
	if runID == (shared.UUID{}) {
		t.Fatalf("Affirm: GetInFlightRunForNode returned zero run id")
	}
}

func testAffirmNodeRunRow_Idempotent(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx); err != nil {
			return err
		}
		return store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
	}); err != nil {
		t.Fatalf("Affirm twice: %v", err)
	}

	var firstID shared.UUID
	var ok bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, found, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, fix.MainRunScopeID)
		firstID = id
		ok = found
		return err
	}); err != nil {
		t.Fatalf("GetInFlightRunForNode: %v", err)
	}
	if !ok || firstID == (shared.UUID{}) {
		t.Fatalf("Affirm idempotent: no in-flight row after two affirms")
	}

	var secondID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, _, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, fix.MainRunScopeID)
		secondID = id
		return err
	}); err != nil {
		t.Fatalf("GetInFlightRunForNode (second call): %v", err)
	}
	if firstID != secondID {
		t.Fatalf("Affirm idempotent: GetInFlightRunForNode returned different ids: %v vs %v", firstID, secondID)
	}
}

func testAffirmNodeRunRow_ErrorsOnClosedScope(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	scopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         scopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		}); err != nil {
			return err
		}
		return store.RunScopes().Close(ctx, tx, scopeID)
	}); err != nil {
		t.Fatalf("Create+Close scope: %v", err)
	}

	err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, scopeID, fix.FrameID, tx)
	})
	if !errors.Is(err, persistence.ErrRunScopeClosed) {
		t.Fatalf("Affirm on closed scope: err = %v, want ErrRunScopeClosed", err)
	}
}

// @blessed-invariant: affirm-node-run-row — AffirmNodeRunRow no-return-value-dependency.
func testAffirmNodeRunRow_NoReturnValueDependency(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	var fn func(context.Context, shared.UUID, shared.UUID, shared.UUID, persistence.Tx) error = store.Nodes().AffirmNodeRunRow

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return fn(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
	}); err != nil {
		t.Fatalf("Affirm via signature-pinned var: %v", err)
	}
}

func testAffirmThenRead(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	var runID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Nodes().AffirmNodeRunRow(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx); err != nil {
			return err
		}
		id, found, err := q.GetInFlightRunForNode(ctx, tx, fix.NodeID, fix.MainRunScopeID)
		if err != nil {
			return err
		}
		if !found {
			t.Fatalf("AffirmThenRead: in-flight row not found after affirm")
		}
		runID = id
		return nil
	}); err != nil {
		t.Fatalf("Affirm+lookup: %v", err)
	}

	var treeRow *persistence.RunTreeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.RunTree().GetByID(ctx, tx, runID)
		treeRow = r
		return err
	}); err != nil {
		t.Fatalf("RunTree.GetByID: %v", err)
	}
	if treeRow == nil {
		t.Fatalf("RunTree.GetByID: row missing")
	}
	if treeRow.Phase != "" && treeRow.Phase != "pending" {
		t.Fatalf("AffirmThenRead: phase = %q, want pending (or empty if projection omits)", treeRow.Phase)
	}
	if string(treeRow.State) != "stale" {
		t.Fatalf("AffirmThenRead: state = %q, want stale", treeRow.State)
	}
	_ = q
}
