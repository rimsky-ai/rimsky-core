// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"reflect"
	"testing"

	"github.com/fallguy/rimsky/foundation/shared"
)

func TestApplyUserdataOverrides(t *testing.T) {
	logger := shared.SilentLogger{}

	t.Run("nil overrides returns clone of base", func(t *testing.T) {
		base := map[string]any{"cli": map[string]any{"k": "v"}}
		got := applyUserdataOverrides(nil, base, nil, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("got %#v want %#v", got, base)
		}
		// Mutating the returned map must not affect base — the no-
		// overrides fast path is now consistent with the merge path:
		// returns a freshly-cloned, owned map.
		got["cli"].(map[string]any)["k"] = "mutated"
		if base["cli"].(map[string]any)["k"] != "v" {
			t.Fatalf("mutating the returned map affected base: %#v", base)
		}
	})

	t.Run("by_executor merged on top of base", func(t *testing.T) {
		base := map[string]any{
			"cli": map[string]any{"silence_timeout_ms": float64(60000)},
		}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{
						"silence_timeout_ms": float64(120000),
						"trace_to":           "/var/traces/run.jsonl",
					},
				},
			},
		}
		got := applyUserdataOverrides(nil, base, ov, "claude-agent", "any-node", logger)
		want := map[string]any{
			"cli": map[string]any{
				"silence_timeout_ms": float64(120000),
				"trace_to":           "/var/traces/run.jsonl",
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("by_node wins over by_executor (most specific)", func(t *testing.T) {
		base := map[string]any{
			"cli": map[string]any{"trace_to": "/base"},
		}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{
						"trace_to":           "/by-executor",
						"synthetic_scenario": "exit-clean",
					},
				},
			},
			"by_node": map[string]any{
				"area-pass": map[string]any{
					"cli": map[string]any{"trace_to": "/by-node"},
				},
			},
		}
		got := applyUserdataOverrides(nil, base, ov, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{
				"trace_to":           "/by-node",   // by_node wins
				"synthetic_scenario": "exit-clean", // contributed by by_executor
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("by_executor entry for a different executor is ignored", func(t *testing.T) {
		base := map[string]any{"cli": map[string]any{"k": "base"}}
		ov := map[string]any{
			"by_executor": map[string]any{
				"http-node": map[string]any{
					"cli": map[string]any{"k": "should-not-apply"},
				},
			},
		}
		got := applyUserdataOverrides(nil, base, ov, "claude-agent", "any", logger)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("got %#v want %#v", got, base)
		}
	})

	t.Run("by_node entry for a different node is ignored", func(t *testing.T) {
		base := map[string]any{"cli": map[string]any{"k": "base"}}
		ov := map[string]any{
			"by_node": map[string]any{
				"reference-pass": map[string]any{
					"cli": map[string]any{"k": "should-not-apply"},
				},
			},
		}
		got := applyUserdataOverrides(nil, base, ov, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("got %#v want %#v", got, base)
		}
	})

	t.Run("nil base + by_executor still produces merged result", func(t *testing.T) {
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{"synthetic_scenario": "exit-clean"},
				},
			},
		}
		got := applyUserdataOverrides(nil, nil, ov, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{"synthetic_scenario": "exit-clean"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("malformed override fragment falls back to base", func(t *testing.T) {
		// lookupFragment requires the (key, subkey) lookup to land on a
		// map[string]any; non-object fragment values produce a (nil,
		// false) miss so the merge is skipped entirely. The validator
		// rejects non-object entries before they reach this path, so
		// this test bypasses validation to confirm the runtime returns
		// base unchanged rather than panicking or producing a non-map
		// root.
		base := map[string]any{"cli": map[string]any{"k": "base"}}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": "non-object",
			},
		}
		got := applyUserdataOverrides(nil, base, ov, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("got %#v want %#v (expected base when override fragment is malformed)", got, base)
		}
	})

	t.Run("nil base + overrides non-empty but no fragment matches → no Warn", func(t *testing.T) {
		// Regression: previously when base was nil and the overrides
		// blob was non-empty but neither by_executor[<executor>] nor
		// by_node[<nodeName>] resolved, `merged` stayed as `any(nil)`,
		// the type-assertion to map[string]any failed, and the
		// "merge produced non-map root" Warn fired erroneously
		// (nothing was actually merged). The fix short-circuits the
		// no-applicable-fragment case to a clone-of-base before the
		// type assertion; this test guards that the Warn does not fire
		// and the function returns an empty map.
		capLog := shared.NewCapturingLogger()
		ov := map[string]any{
			"by_executor": map[string]any{
				"other-exec": map[string]any{
					"cli": map[string]any{"k": "should-not-apply"},
				},
			},
		}
		got := applyUserdataOverrides(nil, nil, ov, "claude-agent", "area-pass", capLog)
		// DeepMergeJSON(nil, nil) yields nil, which type-asserts to a
		// nil map[string]any. Either nil or empty map is acceptable;
		// the load-bearing assertion is "no Warn".
		if len(got) != 0 {
			t.Fatalf("got %#v want empty map", got)
		}
		for _, rec := range capLog.Records() {
			if rec.Level == "warn" {
				t.Fatalf("expected no Warn log, got: %+v", rec)
			}
		}
	})

	t.Run("base is not mutated by merge", func(t *testing.T) {
		base := map[string]any{"cli": map[string]any{"k": "base"}}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{"k": "ov"},
				},
			},
		}
		_ = applyUserdataOverrides(nil, base, ov, "claude-agent", "area-pass", logger)
		// base should still read "base", not the override.
		cli, ok := base["cli"].(map[string]any)
		if !ok || cli["k"] != "base" {
			t.Fatalf("base mutated by applyUserdataOverrides: %#v", base)
		}
	})
}

// TestApplyUserdataOverrides_TemplateDefaultsLayered covers the new
// fourth merge layer added per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// Item 1. Order of increasing specificity:
//
//	templateDefaults
//	  → node.userdata
//	  → instance.userdata_overrides.by_executor
//	  → instance.userdata_overrides.by_node
func TestApplyUserdataOverrides_TemplateDefaultsLayered(t *testing.T) {
	logger := shared.SilentLogger{}

	t.Run("templateDefaults applied when base + overrides are nil", func(t *testing.T) {
		td := map[string]any{"cli": map[string]any{"model": "claude-opus-4-7"}}
		got := applyUserdataOverrides(td, nil, nil, "claude-agent", "area-pass", logger)
		want := map[string]any{"cli": map[string]any{"model": "claude-opus-4-7"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("node.userdata beats templateDefaults on key collision", func(t *testing.T) {
		td := map[string]any{
			"cli": map[string]any{
				"model":              "claude-opus-4-7",
				"handle_rate_limits": true,
			},
		}
		base := map[string]any{
			"cli": map[string]any{
				"model": "claude-opus-4-5", // node overrides
			},
		}
		got := applyUserdataOverrides(td, base, nil, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{
				"model":              "claude-opus-4-5", // node wins
				"handle_rate_limits": true,              // contributed by templateDefaults
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("by_executor override beats templateDefaults", func(t *testing.T) {
		td := map[string]any{
			"cli": map[string]any{
				"model":              "claude-opus-4-7",
				"handle_rate_limits": true,
			},
		}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{"model": "claude-opus-4-8"},
				},
			},
		}
		got := applyUserdataOverrides(td, nil, ov, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{
				"model":              "claude-opus-4-8", // operator override wins
				"handle_rate_limits": true,              // contributed by templateDefaults
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("by_node beats by_executor (regression check)", func(t *testing.T) {
		td := map[string]any{"cli": map[string]any{"model": "claude-opus-4-7"}}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{"model": "claude-opus-4-by-exec"},
				},
			},
			"by_node": map[string]any{
				"area-pass": map[string]any{
					"cli": map[string]any{"model": "claude-opus-4-by-node"},
				},
			},
		}
		got := applyUserdataOverrides(td, nil, ov, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{
				"model": "claude-opus-4-by-node", // by_node wins
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("nested objects deep-merge across all four layers", func(t *testing.T) {
		td := map[string]any{
			"cli": map[string]any{
				"limits": map[string]any{
					"max_corrections": float64(3),
					"max_tokens":      float64(1000),
				},
			},
		}
		base := map[string]any{
			"cli": map[string]any{
				"limits": map[string]any{
					"max_tokens": float64(2000), // node overrides max_tokens
				},
			},
		}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{
						"limits": map[string]any{
							"max_corrections": float64(5), // by_executor overrides max_corrections
						},
					},
				},
			},
			"by_node": map[string]any{
				"area-pass": map[string]any{
					"cli": map[string]any{
						"limits": map[string]any{
							"max_tokens": float64(3000), // by_node overrides max_tokens
						},
					},
				},
			},
		}
		got := applyUserdataOverrides(td, base, ov, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{
				"limits": map[string]any{
					"max_corrections": float64(5),    // by_executor wins
					"max_tokens":      float64(3000), // by_node wins
				},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("arrays at any layer replace", func(t *testing.T) {
		td := map[string]any{
			"cli": map[string]any{
				"allowed_tools": []any{"shell", "search"},
			},
		}
		base := map[string]any{
			"cli": map[string]any{
				"allowed_tools": []any{"write"}, // node-level array replaces
			},
		}
		got := applyUserdataOverrides(td, base, nil, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{
				"allowed_tools": []any{"write"}, // replaced, not concatenated
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})
}
