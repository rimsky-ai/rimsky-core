// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: lineage
// @concept: lineage-record

// @concept: lineage
// @concept: lineage-record
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testLineageQueryByParentRunID(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	parentRunID := shared.UUID(uuid.New())
	childRunID := shared.UUID(uuid.New())
	unrelatedParent := shared.UUID(uuid.New())
	unrelatedChild := shared.UUID(uuid.New())

	insertLeaf := func(t *testing.T, runID, parentID shared.UUID, observedAt time.Time) shared.UUID {
		t.Helper()
		rec := map[string]any{
			"run_id":               runID.String(),
			"frame_id":             fix.FrameID.String(),
			"state":                "fresh",
			"settling_signal_type": "terminal/success",
		}
		if parentID != (shared.UUID{}) {
			rec["parent_run_id"] = parentID.String()
		}
		recBytes, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal lineage record: %v", err)
		}
		rowID := shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Lineage().Insert(ctx, tx, persistence.LineageRow{
				ID:         rowID,
				RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: fix.InstanceID,
				FrameID:    fix.FrameID,
				ObservedAt: observedAt,
				Record:     recBytes,
			})
		}); err != nil {
			t.Fatalf("Lineage.Insert: %v", err)
		}
		return rowID
	}

	base := time.Now().UTC()
	insertLeaf(t, childRunID, parentRunID, base)
	insertLeaf(t, unrelatedChild, unrelatedParent, base.Add(1*time.Second))
	insertLeaf(t, shared.UUID(uuid.New()), shared.UUID{}, base.Add(2*time.Second))

	rows, err := store.Lineage().QueryByParentRunID(ctx, parentRunID, 10)
	if err != nil {
		t.Fatalf("QueryByParentRunID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("QueryByParentRunID: got %d rows want 1 (only the direct child of %s)", len(rows), parentRunID)
	}
	var decoded struct {
		RunID       string `json:"run_id"`
		ParentRunID string `json:"parent_run_id"`
	}
	if err := json.Unmarshal(rows[0].Record, &decoded); err != nil {
		t.Fatalf("unmarshal returned row: %v", err)
	}
	if decoded.RunID != childRunID.String() {
		t.Fatalf("returned row run_id=%q want %q", decoded.RunID, childRunID.String())
	}
	if decoded.ParentRunID != parentRunID.String() {
		t.Fatalf("returned row parent_run_id=%q want %q", decoded.ParentRunID, parentRunID.String())
	}

	rows, err = store.Lineage().QueryByParentRunID(ctx, shared.UUID(uuid.New()), 10)
	if err != nil {
		t.Fatalf("QueryByParentRunID(unknown): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("QueryByParentRunID(unknown): got %d rows want 0", len(rows))
	}
}

func testLineageCountOlderThanMatchesDelete(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	insertLeaf := func(t *testing.T, runID shared.UUID, observedAt time.Time) {
		t.Helper()
		rec := map[string]any{
			"run_id":               runID.String(),
			"frame_id":             fix.FrameID.String(),
			"state":                "fresh",
			"settling_signal_type": "terminal/success",
		}
		recBytes, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal lineage record: %v", err)
		}
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Lineage().Insert(ctx, tx, persistence.LineageRow{
				ID:         shared.UUID(uuid.New()),
				RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: fix.InstanceID,
				FrameID:    fix.FrameID,
				ObservedAt: observedAt,
				Record:     recBytes,
			})
		}); err != nil {
			t.Fatalf("Lineage.Insert: %v", err)
		}
	}

	liveRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	now := time.Now().UTC()
	cutoff := now.Add(-1 * time.Hour)

	insertLeaf(t, shared.UUID(uuid.New()), now.Add(-3*time.Hour))
	insertLeaf(t, shared.UUID(uuid.New()), now.Add(-2*time.Hour))
	insertLeaf(t, shared.UUID(uuid.New()), now.Add(-1*time.Minute))
	insertLeaf(t, liveRunID, now.Add(-4*time.Hour))

	wantPrunable := 2

	gotCount, err := store.Lineage().CountOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("CountOlderThan: %v", err)
	}
	if gotCount != wantPrunable {
		t.Fatalf("CountOlderThan: got %d want %d (2 old rows with no live run)", gotCount, wantPrunable)
	}

	gotDeleted, err := store.Lineage().DeleteOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if gotDeleted != gotCount {
		t.Fatalf("DeleteOlderThan deleted %d but CountOlderThan previewed %d — predicates diverged", gotDeleted, gotCount)
	}

	afterCount, err := store.Lineage().CountOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("CountOlderThan (after delete): %v", err)
	}
	if afterCount != 0 {
		t.Fatalf("CountOlderThan after delete: got %d want 0", afterCount)
	}
}
