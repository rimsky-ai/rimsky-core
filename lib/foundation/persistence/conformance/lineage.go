// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	parentNodeRunID := shared.UUID(uuid.New())
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
			return store.Lineage().Insert(ctx, persistence.LineageRow{
				ID:         rowID,
				RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: fix.InstanceID,
				FrameID:    fix.FrameID,
				ObservedAt: observedAt,
				Record:     recBytes,
			}, tx)
		}); err != nil {
			t.Fatalf("Lineage.Insert: %v", err)
		}
		return rowID
	}

	base := time.Now().UTC()
	insertLeaf(t, childRunID, parentNodeRunID, base)
	insertLeaf(t, unrelatedChild, unrelatedParent, base.Add(1*time.Second))
	insertLeaf(t, shared.UUID(uuid.New()), shared.UUID{}, base.Add(2*time.Second))

	rows, err := store.Lineage().QueryByParentNodeRunID(ctx, parentNodeRunID, 10)
	if err != nil {
		t.Fatalf("QueryByParentNodeRunID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("QueryByParentNodeRunID: got %d rows want 1 (only the direct child of %s)", len(rows), parentNodeRunID)
	}
	var decoded struct {
		NodeRunID       string `json:"run_id"`
		ParentNodeRunID string `json:"parent_run_id"`
	}
	if err := json.Unmarshal(rows[0].Record, &decoded); err != nil {
		t.Fatalf("unmarshal returned row: %v", err)
	}
	if decoded.NodeRunID != childRunID.String() {
		t.Fatalf("returned row run_id=%q want %q", decoded.NodeRunID, childRunID.String())
	}
	if decoded.ParentNodeRunID != parentNodeRunID.String() {
		t.Fatalf("returned row parent_run_id=%q want %q", decoded.ParentNodeRunID, parentNodeRunID.String())
	}

	rows, err = store.Lineage().QueryByParentNodeRunID(ctx, shared.UUID(uuid.New()), 10)
	if err != nil {
		t.Fatalf("QueryByParentNodeRunID(unknown): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("QueryByParentNodeRunID(unknown): got %d rows want 0", len(rows))
	}
}

func testLineageQueryPaginatesWithCursor(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	insertLeaf := func(t *testing.T, runID shared.UUID, observedAt time.Time) shared.UUID {
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
		rowID := shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Lineage().Insert(ctx, persistence.LineageRow{
				ID:         rowID,
				RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: fix.InstanceID,
				FrameID:    fix.FrameID,
				ObservedAt: observedAt,
				Record:     recBytes,
			}, tx)
		}); err != nil {
			t.Fatalf("Lineage.Insert: %v", err)
		}
		return rowID
	}

	base := time.Now().UTC()
	var want []shared.UUID
	for i := 0; i < 5; i++ {
		want = append(want, insertLeaf(t, shared.UUID(uuid.New()), base.Add(time.Duration(i)*time.Second)))
	}

	q := persistence.LineageQuery{InstanceID: &fix.InstanceID, Kind: persistence.LineageRecordKindLeafRun}

	page1, err := store.Lineage().Query(ctx, q, persistence.ListPagination{Limit: 2})
	if err != nil {
		t.Fatalf("Lineage.Query page1: %v", err)
	}
	if len(page1.Rows) != 2 {
		t.Fatalf("Lineage.Query page1: got %d rows want 2", len(page1.Rows))
	}
	if page1.NextCursor == "" {
		t.Fatalf("Lineage.Query page1: NextCursor empty, want a continuation cursor")
	}

	page2, err := store.Lineage().Query(ctx, q, persistence.ListPagination{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("Lineage.Query page2: %v", err)
	}
	if len(page2.Rows) != 2 {
		t.Fatalf("Lineage.Query page2: got %d rows want 2", len(page2.Rows))
	}

	page3, err := store.Lineage().Query(ctx, q, persistence.ListPagination{Limit: 2, Cursor: page2.NextCursor})
	if err != nil {
		t.Fatalf("Lineage.Query page3: %v", err)
	}
	if len(page3.Rows) != 1 {
		t.Fatalf("Lineage.Query page3: got %d rows want 1", len(page3.Rows))
	}
	if page3.NextCursor != "" {
		t.Fatalf("Lineage.Query page3: NextCursor = %q, want empty at end of list", page3.NextCursor)
	}

	seen := map[shared.UUID]struct{}{}
	for _, r := range append(append(page1.Rows, page2.Rows...), page3.Rows...) {
		if _, dup := seen[r.ID]; dup {
			t.Fatalf("Lineage.Query: row %s returned on more than one page", r.ID)
		}
		seen[r.ID] = struct{}{}
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			t.Fatalf("Lineage.Query: row %s missing across all pages", id)
		}
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
			return store.Lineage().Insert(ctx, persistence.LineageRow{
				ID:         shared.UUID(uuid.New()),
				RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: fix.InstanceID,
				FrameID:    fix.FrameID,
				ObservedAt: observedAt,
				Record:     recBytes,
			}, tx)
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
