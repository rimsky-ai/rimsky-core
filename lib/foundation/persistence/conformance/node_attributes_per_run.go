// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testNodeAttributesPerRunInsertByRun(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	data := map[string]any{"k": "v", "n": float64(7)}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runID, fix.NodeID, data, tx)
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var got *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("GetByRun: %v", err)
	}
	if got == nil {
		t.Fatalf("GetByRun: row missing")
	}
	if got.NodeRunID != runID {
		t.Fatalf("GetByRun: NodeRunID=%v want %v", got.NodeRunID, runID)
	}
	if got.NodeID != fix.NodeID {
		t.Fatalf("GetByRun: NodeID=%v want %v", got.NodeID, fix.NodeID)
	}
	if v, _ := got.Data["k"].(string); v != "v" {
		t.Fatalf("GetByRun: data.k=%v want v", got.Data["k"])
	}
	if v, _ := got.Data["n"].(float64); v != 7 {
		t.Fatalf("GetByRun: data.n=%v want 7", got.Data["n"])
	}
}

func testNodeAttributesGetLatestByNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runA := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runA, fix.NodeID, map[string]any{"which": "A"}, tx)
	}); err != nil {
		t.Fatalf("Upsert A: %v", err)
	}
	completeRunAdmin(ctx, t, d, runA)

	time.Sleep(10 * time.Millisecond)
	runB := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if runB == runA {
		t.Fatalf("seedConformanceRunForNode returned the same run twice (%v); "+
			"runA must be settled before runB is seeded so GetLatestByNode has two distinct rows to choose between", runB)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runB, fix.NodeID, map[string]any{"which": "B"}, tx)
	}); err != nil {
		t.Fatalf("Upsert B: %v", err)
	}

	var latest *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetLatestByNode(ctx, fix.NodeID, fix.MainRunScopeID, tx)
		latest = r
		return err
	}); err != nil {
		t.Fatalf("GetLatestByNode: %v", err)
	}
	if latest == nil {
		t.Fatalf("GetLatestByNode: row missing")
	}
	if latest.NodeRunID != runB {
		t.Fatalf("GetLatestByNode: NodeRunID=%v want %v (most-recent run)", latest.NodeRunID, runB)
	}
	if v, _ := latest.Data["which"].(string); v != "B" {
		t.Fatalf("GetLatestByNode: data.which=%v want B", latest.Data["which"])
	}

	missingNodeID := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetLatestByNode(ctx, missingNodeID, fix.MainRunScopeID, tx)
		if err != nil {
			return err
		}
		if r != nil {
			t.Fatalf("GetLatestByNode for unknown node: got row %+v want nil", r)
		}
		return nil
	}); err != nil {
		t.Fatalf("GetLatestByNode (missing): %v", err)
	}
}

func testNodeAttributesCascadeDeleteWithRun(t *testing.T, d persistence.Database, rawExec func(t *testing.T, d persistence.Database, sql string, args ...any)) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runID, fix.NodeID, map[string]any{"k": "v"}, tx)
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var pre *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		pre = r
		return err
	}); err != nil {
		t.Fatalf("GetByRun pre-delete: %v", err)
	}
	if pre == nil {
		t.Fatalf("attribute row missing before delete")
	}

	rawExec(t, d, "DELETE FROM rimsky_node_runs WHERE id = ?", runID)

	var post *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		post = r
		return err
	}); err != nil {
		t.Fatalf("GetByRun post-delete: %v", err)
	}
	if post != nil {
		t.Fatalf("attribute row still present after cascade delete: %+v", post)
	}
}

func testNodeAttributesPerRunDenormConsistency(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runID, fix.NodeID, map[string]any{}, tx)
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var attrRow *persistence.NodeAttributesRow
	var runRow *persistence.NodeRunTreeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		a, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		if err != nil {
			return err
		}
		attrRow = a
		r, err := store.NodeRunTree().GetByID(ctx, tx, runID)
		runRow = r
		return err
	}); err != nil {
		t.Fatalf("read attr/run: %v", err)
	}
	if attrRow == nil {
		t.Fatalf("attribute row missing")
	}
	if runRow == nil {
		t.Fatalf("run row missing")
	}
	if attrRow.NodeID != runRow.NodeID {
		t.Fatalf("denormalized node_id mismatch: attr=%v run=%v", attrRow.NodeID, runRow.NodeID)
	}
}
