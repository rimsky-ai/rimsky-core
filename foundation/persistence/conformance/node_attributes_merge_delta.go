// node_attributes_merge_delta.go — NodeAttributesMergeDelta conformance area.
//
// Covers NodeAttributesStore.MergeDelta:
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

	"github.com/fallguy/rimsky/foundation/persistence"
)

func testNodeAttributesMergeDelta(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Store()

	// ---- Missing row: MergeDelta returns wrapped ErrNotFound ----
	missingNodeID := uuid.New()
	err := store.NodeAttributes().MergeDelta(ctx, missingNodeID,
		map[string]any{"k": "v"}, nil)
	if err == nil {
		t.Fatalf("MergeDelta on missing row: expected error, got nil")
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("MergeDelta on missing row: error does not wrap persistence.ErrNotFound: %v", err)
	}

	// Seed an attributes row for fix.NodeID.
	initial := map[string]any{
		"top1": "v1",
		"nested": map[string]any{
			"a": float64(1),
			"b": float64(2),
		},
	}
	if err := store.NodeAttributes().Upsert(ctx, fix.NodeID, 0, initial, nil); err != nil {
		t.Fatalf("Upsert seed: %v", err)
	}

	// ---- Shallow merge: top-level keys overwrite wholesale ----
	delta := map[string]any{
		"top2": "v2",
		"nested": map[string]any{
			"a": float64(99),
			// note: missing b — under shallow merge the new value
			// replaces the prior wholesale, so b should be gone.
		},
	}
	if err := store.NodeAttributes().MergeDelta(ctx, fix.NodeID, delta, nil); err != nil {
		t.Fatalf("MergeDelta shallow: %v", err)
	}
	got, err := store.NodeAttributes().Get(ctx, fix.NodeID, nil)
	if err != nil {
		t.Fatalf("Get after shallow merge: %v", err)
	}
	if got == nil {
		t.Fatalf("Get after shallow merge: row missing")
	}
	// top1 retained, top2 added, nested key replaced wholesale.
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

	// ---- nil-delta touch path: row exists -> updated_at bumps, data unchanged ----
	priorUpdatedAt := got.UpdatedAt
	priorData := got.Data
	// Sleep a small amount so the time comparison can move forward
	// regardless of underlying clock granularity (Postgres NOW() is
	// microsecond-resolution; SQLite's nowUTC() is RFC3339Nano).
	time.Sleep(10 * time.Millisecond)
	if err := store.NodeAttributes().MergeDelta(ctx, fix.NodeID, nil, nil); err != nil {
		t.Fatalf("MergeDelta nil-delta touch: %v", err)
	}
	got2, err := store.NodeAttributes().Get(ctx, fix.NodeID, nil)
	if err != nil {
		t.Fatalf("Get after nil-delta touch: %v", err)
	}
	if got2 == nil {
		t.Fatalf("Get after nil-delta touch: row missing")
	}
	if !got2.UpdatedAt.After(priorUpdatedAt) {
		t.Fatalf("nil-delta touch: updated_at did not advance (prior=%v current=%v)",
			priorUpdatedAt, got2.UpdatedAt)
	}
	// Data unchanged.
	if len(got2.Data) != len(priorData) {
		t.Fatalf("nil-delta touch: data shape changed (prior=%d keys, current=%d keys)",
			len(priorData), len(got2.Data))
	}
	for k, v := range priorData {
		if !equalAny(got2.Data[k], v) {
			t.Fatalf("nil-delta touch: data[%q] mutated (was %v, is %v)", k, v, got2.Data[k])
		}
	}

	// ---- nil-delta on missing row: silent no-op (no error) ----
	missingNodeID2 := uuid.New()
	if err := store.NodeAttributes().MergeDelta(ctx, missingNodeID2, nil, nil); err != nil {
		t.Fatalf("MergeDelta nil-delta on missing row: expected silent no-op, got %v", err)
	}
}

// equalAny returns true if a and b are deeply equal under Go's
// json.Unmarshal-into-any decoding. We can't import reflect.DeepEqual at
// the package level without bringing in the universe, but the values we
// store here are constrained enough that recursing on jsonValueEqual
// suffices.
func equalAny(a, b any) bool { return jsonValueEqual(a, b) }
