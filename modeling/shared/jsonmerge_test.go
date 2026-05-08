// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import (
	"reflect"
	"testing"
)

func TestDeepMergeJSON(t *testing.T) {
	tests := []struct {
		name string
		base any
		over any
		want any
	}{
		{
			name: "empty over returns base",
			base: map[string]any{"a": 1},
			over: map[string]any{},
			want: map[string]any{"a": 1},
		},
		{
			name: "nil over returns base clone",
			base: map[string]any{"a": 1},
			over: nil,
			want: map[string]any{"a": 1},
		},
		{
			name: "nil base + map over returns over clone",
			base: nil,
			over: map[string]any{"a": 1},
			want: map[string]any{"a": 1},
		},
		{
			name: "scalar over replaces map base wholesale",
			base: map[string]any{"a": 1},
			over: "replaced",
			want: "replaced",
		},
		{
			name: "map over replaces scalar base wholesale",
			base: "old",
			over: map[string]any{"a": 1},
			want: map[string]any{"a": 1},
		},
		{
			name: "nested object recurses",
			base: map[string]any{
				"cli": map[string]any{
					"silence_timeout_ms": float64(60000),
					"trace_to":           "/old",
				},
			},
			over: map[string]any{
				"cli": map[string]any{
					"trace_to":           "/new",
					"synthetic_scenario": "exit-clean",
				},
			},
			want: map[string]any{
				"cli": map[string]any{
					"silence_timeout_ms": float64(60000),
					"trace_to":           "/new",
					"synthetic_scenario": "exit-clean",
				},
			},
		},
		{
			name: "shape mismatch — object on object, scalar on object — over wins",
			base: map[string]any{"cli": map[string]any{"k": 1}},
			over: map[string]any{"cli": "string-now"},
			want: map[string]any{"cli": "string-now"},
		},
		{
			name: "arrays replace; do not concatenate",
			base: map[string]any{"items": []any{1.0, 2.0}},
			over: map[string]any{"items": []any{9.0}},
			want: map[string]any{"items": []any{9.0}},
		},
		{
			// Operators can realistically swap shapes: change a list to
			// an object or vice versa. Verify documented behaviour
			// (over wins) holds in both directions.
			name: "shape mismatch — array base, map override → map wins",
			base: map[string]any{"k": []any{1.0, 2.0}},
			over: map[string]any{"k": map[string]any{"x": 1.0}},
			want: map[string]any{"k": map[string]any{"x": 1.0}},
		},
		{
			name: "shape mismatch — map base, array override → array wins",
			base: map[string]any{"k": map[string]any{"x": 1.0}},
			over: map[string]any{"k": []any{1.0, 2.0}},
			want: map[string]any{"k": []any{1.0, 2.0}},
		},
		{
			name: "additional sibling keys are preserved",
			base: map[string]any{
				"cli":   map[string]any{"k": 1},
				"other": "kept",
			},
			over: map[string]any{
				"cli":     map[string]any{"k": 2},
				"another": "added",
			},
			want: map[string]any{
				"cli":     map[string]any{"k": 2},
				"other":   "kept",
				"another": "added",
			},
		},
		{
			name: "deep nesting (3 levels) merges per level",
			base: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c1": "old",
						"c2": "kept",
					},
				},
			},
			over: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c1": "new",
						"c3": "added",
					},
				},
			},
			want: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c1": "new",
						"c2": "kept",
						"c3": "added",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeepMergeJSON(tt.base, tt.over)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("DeepMergeJSON mismatch:\n got = %#v\nwant = %#v", got, tt.want)
			}
		})
	}
}

// TestDeepMergeJSON_DoesNotMutateInputs guards against the easy bug
// where the merge writes through to a shared map node. Userdata
// overrides are read from persistence per dispatch; if merge mutated
// either layer, a second dispatch would see a polluted base.
func TestDeepMergeJSON_DoesNotMutateInputs(t *testing.T) {
	base := map[string]any{
		"cli": map[string]any{"k": "from-base"},
	}
	over := map[string]any{
		"cli": map[string]any{"k": "from-over", "extra": "added"},
	}
	baseCopy := cloneJSON(base)
	overCopy := cloneJSON(over)

	_ = DeepMergeJSON(base, over)

	if !reflect.DeepEqual(base, baseCopy) {
		t.Fatalf("base mutated: got %#v want %#v", base, baseCopy)
	}
	if !reflect.DeepEqual(over, overCopy) {
		t.Fatalf("over mutated: got %#v want %#v", over, overCopy)
	}
}

// TestDeepMergeJSON_ArrayElementsAreCloned guards against returning a
// result that shares slice headers with the inputs. Mutating the
// returned slice must not affect the input.
func TestDeepMergeJSON_ArrayElementsAreCloned(t *testing.T) {
	base := map[string]any{"k": []any{"a", "b"}}
	got := DeepMergeJSON(base, map[string]any{}).(map[string]any)
	gotSlice := got["k"].([]any)
	gotSlice[0] = "MUTATED"
	if base["k"].([]any)[0] != "a" {
		t.Fatalf("base slice mutated through return")
	}
}
