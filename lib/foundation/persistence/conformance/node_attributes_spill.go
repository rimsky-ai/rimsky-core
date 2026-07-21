// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testNodeAttributesSpillUpsertMergeDeltaOrphans(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	attrs := store.NodeAttributes()
	orphans := store.BlobOrphans()

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour)

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	small := map[string]any{"k": "v"}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, fix.NodeID, small, tx)
	}); err != nil {
		t.Fatalf("Upsert small: %v", err)
	}
	got := mustGetByRun(t, ctx, store, runID)
	if got.Data["k"] != "v" {
		t.Fatalf("Upsert small: got %v, want k=v", got.Data)
	}

	bigVal := strings.Repeat("x", 500)
	large := map[string]any{"big": bigVal, "tag": "first"}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, fix.NodeID, large, tx)
	}); err != nil {
		t.Fatalf("Upsert large: %v", err)
	}
	got = mustGetByRun(t, ctx, store, runID)
	if got.Data["big"] != bigVal || got.Data["tag"] != "first" {
		t.Fatalf("Upsert large: round-trip mismatch: %+v", got.Data)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return attrs.MergeDelta(ctx, runID, map[string]any{"tag": "second"}, tx)
	}); err != nil {
		t.Fatalf("MergeDelta on spilled row: %v", err)
	}
	got = mustGetByRun(t, ctx, store, runID)
	if got.Data["big"] != bigVal {
		t.Fatalf("MergeDelta on spilled row: big lost: %+v", got.Data)
	}
	if got.Data["tag"] != "second" {
		t.Fatalf("MergeDelta on spilled row: tag=%v, want second", got.Data["tag"])
	}

	large2 := map[string]any{"big": bigVal, "tag": "third"}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, fix.NodeID, large2, tx)
	}); err != nil {
		t.Fatalf("Upsert large2 (replaces spilled handle): %v", err)
	}

	tiny := map[string]any{"k": "v"}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, fix.NodeID, tiny, tx)
	}); err != nil {
		t.Fatalf("Upsert tiny (downgrades from spilled): %v", err)
	}
	got = mustGetByRun(t, ctx, store, runID)
	if got.Data["k"] != "v" || len(got.Data) != 1 {
		t.Fatalf("Upsert tiny: got %+v, want exactly k=v", got.Data)
	}

	orphRows, err := orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), mem.Name(), 100)
	if err != nil {
		t.Fatalf("orphans.DueBefore: %v", err)
	}
	if len(orphRows) < 2 {
		t.Fatalf("expected at least 2 replaced spill handles enrolled as orphans (first Upsert-large + "+
			"large2 + downgrade), got %d: %+v", len(orphRows), orphRows)
	}
	for _, r := range orphRows {
		if r.Backend != mem.Name() {
			t.Fatalf("orphan row backend: got %q, want %q", r.Backend, mem.Name())
		}
	}
}

func mustGetByRun(t *testing.T, ctx context.Context, store persistence.Tables, runID shared.UUID) *persistence.NodeAttributesRow {
	t.Helper()
	var out *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		out = r
		return err
	}); err != nil {
		t.Fatalf("GetByRun: %v", err)
	}
	if out == nil {
		t.Fatalf("GetByRun: row missing")
	}
	return out
}
