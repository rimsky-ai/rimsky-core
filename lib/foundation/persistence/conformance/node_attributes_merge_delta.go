// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

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

	delta := map[string]any{
		"top2": "v2",
		"nested": map[string]any{
			"a": float64(99),
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

	priorUpdatedAt := got.UpdatedAt
	priorData := got.Data
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
		if !jsonValueEqual(got2.Data[k], v) {
			t.Fatalf("nil-delta touch: data[%q] mutated (was %v, is %v)", k, v, got2.Data[k])
		}
	}

	missingRunID2 := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().MergeDelta(ctx, missingRunID2, nil, tx)
	}); err != nil {
		t.Fatalf("MergeDelta nil-delta on missing row: expected silent no-op, got %v", err)
	}
}
