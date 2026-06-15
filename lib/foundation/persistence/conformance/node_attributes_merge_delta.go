// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: NodeAttributesMergeDelta conformance area.
// Covers NodeAttributeTable.MergeDelta:
//
//   - shallow merge with nested keys (top-level keys overwrite, but the
//     value at each top-level key is replaced wholesale, not deep-merged)
//   - nil-delta touch path (bumps updated_at; no-op when row absent)
//   - missing-row case returns a wrapped persistence.ErrNotFound (verified
//     via errors.Is so both drivers must agree on the sentinel)
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testNodeAttributesMergeDelta(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// @constraint: every NodeAttributes row keys on a real node_run_id; seed one for this run before exercising MergeDelta.
	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	// @constraint: MergeDelta against an absent row must surface a wrapped persistence.ErrNotFound (both drivers agree on the sentinel).
	missingRunID := uuid.New()
	err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().MergeDelta(ctx, missingRunID,
			map[string]any{"k": "v"}, tx)
	})
	if err == nil {
		t.Fatalf("MergeDelta on missing row: expected error, got nil")
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("MergeDelta on missing row: error does not wrap persistence.ErrNotFound: %v", err)
	}

	initial := map[string]any{
		"top1": "v1",
		"nested": map[string]any{
			"a": float64(1),
			"b": float64(2),
		},
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runID, fix.NodeID, initial, tx)
	}); err != nil {
		t.Fatalf("Upsert seed: %v", err)
	}

	// @constraint: shallow merge — top-level keys overwrite wholesale; existing untouched top-level keys are retained.
	delta := map[string]any{
		"top2": "v2",
		"nested": map[string]any{
			"a": float64(99),
			// @deliberate: b is omitted so the wholesale replacement of "nested" must drop the prior b entry.
		},
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().MergeDelta(ctx, runID, delta, tx)
	}); err != nil {
		t.Fatalf("MergeDelta shallow: %v", err)
	}
	var got *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get after shallow merge: %v", err)
	}
	if got == nil {
		t.Fatalf("Get after shallow merge: row missing")
	}
	if v, _ := got.Data["top1"].(string); v != "v1" {
		t.Fatalf("shallow merge: top1 = %v want v1 (existing top-level keys must be retained)", got.Data["top1"])
	}
	if v, _ := got.Data["top2"].(string); v != "v2" {
		t.Fatalf("shallow merge: top2 = %v want v2 (new top-level key must land)", got.Data["top2"])
	}
	nestedAny, ok := got.Data["nested"].(map[string]any)
	if !ok {
		t.Fatalf("shallow merge: nested is not map[string]any: %T", got.Data["nested"])
	}
	if v, _ := nestedAny["a"].(float64); v != 99 {
		t.Fatalf("shallow merge: nested.a = %v want 99", nestedAny["a"])
	}
	if _, present := nestedAny["b"]; present {
		t.Fatalf("shallow merge: nested.b still present after wholesale replace; got %v", nestedAny["b"])
	}

	// @constraint: nil-delta on an existing row bumps updated_at without mutating data.
	priorUpdatedAt := got.UpdatedAt
	priorData := got.Data
	// @deliberate: sleep so updated_at can advance under both backends — Postgres NOW() is microsecond-resolution and SQLite's nowUTC() is RFC3339Nano.
	time.Sleep(10 * time.Millisecond)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().MergeDelta(ctx, runID, nil, tx)
	}); err != nil {
		t.Fatalf("MergeDelta nil-delta touch: %v", err)
	}
	var got2 *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		got2 = r
		return err
	}); err != nil {
		t.Fatalf("Get after nil-delta touch: %v", err)
	}
	if got2 == nil {
		t.Fatalf("Get after nil-delta touch: row missing")
	}
	if !got2.UpdatedAt.After(priorUpdatedAt) {
		t.Fatalf("nil-delta touch: updated_at did not advance (prior=%v current=%v)",
			priorUpdatedAt, got2.UpdatedAt)
	}
	if len(got2.Data) != len(priorData) {
		t.Fatalf("nil-delta touch: data shape changed (prior=%d keys, current=%d keys)",
			len(priorData), len(got2.Data))
	}
	for k, v := range priorData {
		if !equalAny(got2.Data[k], v) {
			t.Fatalf("nil-delta touch: data[%q] mutated (was %v, is %v)", k, v, got2.Data[k])
		}
	}

	// @constraint: nil-delta against an absent row is a silent no-op (no error), distinct from the ErrNotFound path above.
	missingRunID2 := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().MergeDelta(ctx, missingRunID2, nil, tx)
	}); err != nil {
		t.Fatalf("MergeDelta nil-delta on missing row: expected silent no-op, got %v", err)
	}
}

// equalAny returns true if a and b are deeply equal under Go's
// json.Unmarshal-into-any decoding. We can't import reflect.DeepEqual at
// the package level without bringing in the universe, but the values we
// store here are constrained enough that recursing on jsonValueEqual
// suffices.
func equalAny(a, b any) bool { return jsonValueEqual(a, b) }
