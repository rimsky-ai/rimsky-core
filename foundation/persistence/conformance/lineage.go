// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// lineage.go — LineageTable conformance fixture. Exercises the
// content-lineage projection against both drivers (postgres + sqlite)
// so the per-driver JSON-path predicate (`record->>'parent_run_id'`
// for postgres / `json_extract(record, '$.parent_run_id')` for sqlite)
// is honored by the real engine — not just the in-memory test fakes.
//
//	@concept: lineage
//	@concept: lineage-record
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// testLineageQueryByParentRunID round-trips a leaf_run row with a
// non-empty parent_run_id and pins that QueryByParentRunID returns it.
// Without this conformance check the per-driver predicate
// (`record->>'parent_run_id' = $1` postgres / `json_extract(...)`
// sqlite) is only exercised by the in-memory fake in
// `runtime/lineage_writer_test.go::queryableFakeLineageTable`, which
// re-parses JSON in-process and can't catch a typo in the SQL.
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
	// child of parentRunID — must be returned.
	insertLeaf(t, childRunID, parentRunID, base)
	// child of a different parent — must NOT be returned for parentRunID.
	insertLeaf(t, unrelatedChild, unrelatedParent, base.Add(1*time.Second))
	// root (no parent) — must NOT be returned (predicate is `=`).
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

	// QueryByParentRunID on an unknown parent must return no rows
	// (negative case — guards against a buggy predicate that always
	// matches).
	rows, err = store.Lineage().QueryByParentRunID(ctx, shared.UUID(uuid.New()), 10)
	if err != nil {
		t.Fatalf("QueryByParentRunID(unknown): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("QueryByParentRunID(unknown): got %d rows want 0", len(rows))
	}
}
