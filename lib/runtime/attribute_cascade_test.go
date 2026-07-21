// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import "testing"

func TestDiffAttributesData_ChangedValueIncluded(t *testing.T) {
	prior := map[string]any{"a": 1.0}
	current := map[string]any{"a": 2.0}
	changes := diffAttributesData(prior, current)
	if got, ok := changes["a"]; !ok || got != 2.0 {
		t.Fatalf("changes[a] = %v, ok=%v; want 2.0, true", got, ok)
	}
}

func TestDiffAttributesData_UnchangedValueOmitted(t *testing.T) {
	prior := map[string]any{"a": 1.0}
	current := map[string]any{"a": 1.0}
	changes := diffAttributesData(prior, current)
	if _, ok := changes["a"]; ok {
		t.Fatalf("changes[a] present, want omitted for an unchanged value")
	}
}

func TestDiffAttributesData_NewKeyIncluded(t *testing.T) {
	prior := map[string]any{}
	current := map[string]any{"b": "hello"}
	changes := diffAttributesData(prior, current)
	if got, ok := changes["b"]; !ok || got != "hello" {
		t.Fatalf("changes[b] = %v, ok=%v; want hello, true", got, ok)
	}
}

func TestDiffAttributesData_DeletedKeyEmitsNil(t *testing.T) {
	prior := map[string]any{"c": "gone"}
	current := map[string]any{}
	changes := diffAttributesData(prior, current)
	v, ok := changes["c"]
	if !ok {
		t.Fatalf("changes[c] missing, want a nil entry for a deleted key")
	}
	if v != nil {
		t.Fatalf("changes[c] = %v, want nil for a deleted key", v)
	}
}

func TestAttributesValueEqual_StructurallyEqualMapsMatch(t *testing.T) {
	a := map[string]any{"x": 1.0, "y": []any{"a", "b"}}
	b := map[string]any{"x": 1.0, "y": []any{"a", "b"}}
	if !attributesValueEqual(a, b) {
		t.Fatalf("expected structurally identical values to compare equal")
	}
}

func TestAttributesValueEqual_DifferentValuesMismatch(t *testing.T) {
	if attributesValueEqual(map[string]any{"x": 1.0}, map[string]any{"x": 2.0}) {
		t.Fatalf("expected differing values to compare unequal")
	}
}

func TestAttributesValueEqual_UnmarshalableValueIsUnequal(t *testing.T) {
	if attributesValueEqual(make(chan int), make(chan int)) {
		t.Fatalf("expected unmarshalable values to compare unequal rather than panic")
	}
}
