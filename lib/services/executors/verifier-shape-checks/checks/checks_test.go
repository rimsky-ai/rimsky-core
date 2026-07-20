// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package checks

import "testing"

func TestNoNulls(t *testing.T) {
	rows := []Row{{"a": 1}, {"a": 2}, {"a": nil}}
	r := Run(CheckSpec{Kind: "no_nulls", Config: map[string]any{"field": "a"}}, rows)
	if r.Pass {
		t.Errorf("expected fail, got: %+v", r)
	}
	rows2 := []Row{{"a": 1}, {"a": 2}}
	if r := Run(CheckSpec{Kind: "no_nulls", Config: map[string]any{"field": "a"}}, rows2); !r.Pass {
		t.Errorf("expected pass, got: %+v", r)
	}
}

func TestNullableFieldsPresent(t *testing.T) {
	rows := []Row{{"a": 1, "b": nil}, {"a": 2}}
	r := Run(CheckSpec{Kind: "nullable_fields_present", Config: map[string]any{"field": "b"}}, rows)
	if r.Pass {
		t.Errorf("expected fail (missing b in row 2): %+v", r)
	}
}

func TestPKUnique(t *testing.T) {
	rows := []Row{{"id": "x"}, {"id": "y"}, {"id": "x"}}
	r := Run(CheckSpec{Kind: "pk_unique", Config: map[string]any{"field": "id"}}, rows)
	if r.Pass {
		t.Errorf("expected fail (duplicate id): %+v", r)
	}
}

func TestRowCountRatio(t *testing.T) {
	rows := make([]Row, 10)
	for i := range rows {
		rows[i] = Row{"a": i}
	}
	r := Run(CheckSpec{Kind: "row_count_ratio", Config: map[string]any{"baseline": float64(8), "low": 0.5, "high": 2.0}}, rows)
	if !r.Pass {
		t.Errorf("expected pass (10/8 = 1.25): %+v", r)
	}
	r = Run(CheckSpec{Kind: "row_count_ratio", Config: map[string]any{"baseline": float64(100), "low": 0.5, "high": 2.0}}, rows)
	if r.Pass {
		t.Errorf("expected fail (10/100 = 0.1): %+v", r)
	}
}

func TestRowCountAbsolute(t *testing.T) {
	rows := []Row{{"a": 1}, {"a": 2}}
	r := Run(CheckSpec{Kind: "row_count_absolute", Config: map[string]any{"min": float64(1)}}, rows)
	if !r.Pass {
		t.Errorf("expected pass: %+v", r)
	}
	r = Run(CheckSpec{Kind: "row_count_absolute", Config: map[string]any{"min": float64(5)}}, rows)
	if r.Pass {
		t.Errorf("expected fail (need 5+): %+v", r)
	}
}

func TestValueInSet(t *testing.T) {
	rows := []Row{{"k": "a"}, {"k": "b"}}
	r := Run(CheckSpec{Kind: "value_in_set", Config: map[string]any{"field": "k", "set": []any{"a", "b", "c"}}}, rows)
	if !r.Pass {
		t.Errorf("expected pass: %+v", r)
	}
	r = Run(CheckSpec{Kind: "value_in_set", Config: map[string]any{"field": "k", "set": []any{"x", "y"}}}, rows)
	if r.Pass {
		t.Errorf("expected fail: %+v", r)
	}
}

func TestRegexMatch(t *testing.T) {
	rows := []Row{{"k": "abc"}, {"k": "xyz"}}
	r := Run(CheckSpec{Kind: "regex_match", Config: map[string]any{"field": "k", "pattern": "^[a-z]+$"}}, rows)
	if !r.Pass {
		t.Errorf("expected pass: %+v", r)
	}
	r = Run(CheckSpec{Kind: "regex_match", Config: map[string]any{"field": "k", "pattern": "^[0-9]+$"}}, rows)
	if r.Pass {
		t.Errorf("expected fail: %+v", r)
	}
}

func TestNumericRange(t *testing.T) {
	rows := []Row{{"n": float64(5)}, {"n": float64(10)}}
	r := Run(CheckSpec{Kind: "numeric_range", Config: map[string]any{"field": "n", "min": float64(0), "max": float64(20)}}, rows)
	if !r.Pass {
		t.Errorf("expected pass: %+v", r)
	}
	r = Run(CheckSpec{Kind: "numeric_range", Config: map[string]any{"field": "n", "min": float64(0), "max": float64(7)}}, rows)
	if r.Pass {
		t.Errorf("expected fail (10 > 7): %+v", r)
	}
}

func TestUnknownKind(t *testing.T) {
	r := Run(CheckSpec{Kind: "nope"}, nil)
	if r.Pass {
		t.Errorf("unknown kind should fail")
	}
	if !r.ConfigError {
		t.Errorf("unknown check kind is a misconfiguration, not a data-quality failure: %+v", r)
	}
}

func TestNumericRange_NonNumericBoundIsConfigErrorNotVacuousPass(t *testing.T) {
	rows := []Row{{"n": float64(100)}, {"n": float64(-100)}}
	r := Run(CheckSpec{Kind: "numeric_range", Config: map[string]any{"field": "n", "min": "10"}}, rows)
	if r.Pass {
		t.Fatalf("a mistyped (quoted) min bound must not silently disable the bound and vacuously pass rows: %+v", r)
	}
	if !r.ConfigError {
		t.Errorf("a non-numeric min bound is a misconfiguration, not a per-row failure: %+v", r)
	}

	r = Run(CheckSpec{Kind: "numeric_range", Config: map[string]any{"field": "n", "max": "10"}}, rows)
	if r.Pass || !r.ConfigError {
		t.Errorf("a non-numeric max bound must also be reported as ConfigError: %+v", r)
	}
}

func TestConfigErrorFlaggedForEveryMisconfigurationPath(t *testing.T) {
	cases := []struct {
		name string
		spec CheckSpec
	}{
		{"no_nulls_missing_field", CheckSpec{Kind: "no_nulls", Config: map[string]any{}}},
		{"nullable_fields_present_missing_field", CheckSpec{Kind: "nullable_fields_present", Config: map[string]any{}}},
		{"pk_unique_missing_field", CheckSpec{Kind: "pk_unique", Config: map[string]any{}}},
		{"row_count_ratio_missing_baseline", CheckSpec{Kind: "row_count_ratio", Config: map[string]any{}}},
		{"row_count_ratio_bad_baseline", CheckSpec{Kind: "row_count_ratio", Config: map[string]any{"baseline": -1}}},
		{"row_count_absolute_missing_min", CheckSpec{Kind: "row_count_absolute", Config: map[string]any{}}},
		{"value_in_set_missing_field", CheckSpec{Kind: "value_in_set", Config: map[string]any{}}},
		{"value_in_set_missing_set", CheckSpec{Kind: "value_in_set", Config: map[string]any{"field": "x"}}},
		{"regex_match_missing_field", CheckSpec{Kind: "regex_match", Config: map[string]any{}}},
		{"regex_match_missing_pattern", CheckSpec{Kind: "regex_match", Config: map[string]any{"field": "x"}}},
		{"regex_match_bad_pattern", CheckSpec{Kind: "regex_match", Config: map[string]any{"field": "x", "pattern": "("}}},
		{"numeric_range_missing_field", CheckSpec{Kind: "numeric_range", Config: map[string]any{}}},
		{"numeric_range_missing_bounds", CheckSpec{Kind: "numeric_range", Config: map[string]any{"field": "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Run(tc.spec, nil)
			if r.Pass {
				t.Fatalf("expected a failing Result for a misconfigured check: %+v", r)
			}
			if !r.ConfigError {
				t.Errorf("expected ConfigError=true for a misconfigured check, got %+v", r)
			}
		})
	}
}
