// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package checkspec

import "testing"

func TestFieldList_SingleField(t *testing.T) {
	got, err := FieldList(map[string]any{"field": "id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "id" {
		t.Fatalf("got %v", got)
	}
}

func TestFieldList_FieldsArray(t *testing.T) {
	got, err := FieldList(map[string]any{"fields": []any{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestFieldList_FieldsStringSlice(t *testing.T) {
	got, err := FieldList(map[string]any{"fields": []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestFieldList_RejectsNonStringEntry(t *testing.T) {
	if _, err := FieldList(map[string]any{"fields": []any{"a", 1}}); err == nil {
		t.Fatal("expected error for non-string fields entry")
	}
}

func TestFieldList_RequiresFieldOrFields(t *testing.T) {
	if _, err := FieldList(map[string]any{}); err == nil {
		t.Fatal("expected error when neither field nor fields is set")
	}
}

func TestFieldList_RejectsEmptyFieldsArray(t *testing.T) {
	if _, err := FieldList(map[string]any{"fields": []any{}}); err == nil {
		t.Fatal("expected error for an empty fields: [] array (would compile syntactically invalid SQL)")
	}
}

func TestFieldList_RejectsEmptyFieldsStringSlice(t *testing.T) {
	if _, err := FieldList(map[string]any{"fields": []string{}}); err == nil {
		t.Fatal("expected error for an empty fields string slice")
	}
}

func TestNumeric_CoversAllNumericKinds(t *testing.T) {
	cases := []any{
		float64(1), float32(1), int(1), int8(1), int16(1), int32(1), int64(1),
		uint(1), uint8(1), uint16(1), uint32(1), uint64(1),
	}
	for _, c := range cases {
		got, ok := Numeric(c)
		if !ok || got != 1 {
			t.Errorf("Numeric(%v (%T)) = %v, %v; want 1, true", c, c, got, ok)
		}
	}
}

func TestNumeric_RejectsNonNumeric(t *testing.T) {
	if _, ok := Numeric("10"); ok {
		t.Fatal("a quoted numeric string must not be treated as numeric")
	}
	if _, ok := Numeric(nil); ok {
		t.Fatal("nil must not be treated as numeric")
	}
}

func TestNumericDefault_FallsBackOnNonNumeric(t *testing.T) {
	got, ok := NumericDefault("bogus", 42)
	if ok {
		t.Fatal("expected ok=false when falling back to the default")
	}
	if got != 42 {
		t.Fatalf("got %v, want 42", got)
	}
	got, ok = NumericDefault(float64(7), 42)
	if !ok || got != 7 {
		t.Fatalf("got %v, %v; want 7, true", got, ok)
	}
}
