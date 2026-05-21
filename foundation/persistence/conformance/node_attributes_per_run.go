// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_attributes_per_run.go — NodeAttributesPerRun conformance area.
//
// Exercises the per-run keying of `rimsky_node_attributes` (post
// 2026-05-20). Covers:
//
//   - Insert-by-run and GetByRun round-trip.
//   - GetLatestByNode returns the most-recent run's row.
//   - Cascade delete: dropping the run row drops the attribute row.
//   - Denormalized node_id matches the run's canonical node_id.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// testNodeAttributesPerRunInsertByRun verifies that Upsert(runID, nodeID, ...)
// followed by GetByRun(runID) round-trips data and reads back both ids.
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

// testNodeAttributesGetLatestByNode verifies that GetLatestByNode returns
// the row with the most-recent updated_at when multiple runs exist.
func testNodeAttributesGetLatestByNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// Two runs for the same node.
	runA := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runA, fix.NodeID, map[string]any{"which": "A"}, tx)
	}); err != nil {
		t.Fatalf("Upsert A: %v", err)
	}

	// Sleep to ensure the second row's updated_at strictly exceeds the
	// first's, regardless of underlying clock granularity.
	time.Sleep(10 * time.Millisecond)
	runB := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runB, fix.NodeID, map[string]any{"which": "B"}, tx)
	}); err != nil {
		t.Fatalf("Upsert B: %v", err)
	}

	var latest *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetLatestByNode(ctx, fix.NodeID, tx)
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

	// Sanity: GetLatestByNode for a node with no rows returns (nil, nil).
	missingNodeID := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetLatestByNode(ctx, missingNodeID, tx)
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

// testNodeAttributesCascadeDeleteWithRun verifies that deleting the
// underlying rimsky_node_runs row cascades to the attribute row.
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

	// Confirm row exists.
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

	// Delete the run row via raw SQL. RunTreeTable has no Delete by
	// design — raw SQL is the right approach for this test.
	rawExec(t, d, "DELETE FROM rimsky_node_runs WHERE id = ?", runID)

	// Attribute row should be gone via ON DELETE CASCADE.
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

// testNodeAttributesPerRunDenormConsistency verifies that the
// denormalized node_id on the attribute row matches the run's canonical
// node_id from rimsky_node_runs.
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
	var runRow *persistence.RunTreeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		a, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		if err != nil {
			return err
		}
		attrRow = a
		r, err := store.RunTree().GetByID(ctx, tx, runID)
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
