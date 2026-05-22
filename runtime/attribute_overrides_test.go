// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"reflect"
	"testing"

	"github.com/fallguy/rimsky/foundation/shared"
)

// TestApplyAttributeOverrides covers the L3 + L4 runtime merge layers.
// L1 (template defaults) folded into the effective schema at
// registration; L2 (per-node schema) lives inside the resolved bag
// already — both have happened before this function is called.
func TestApplyAttributeOverrides(t *testing.T) {
	logger := shared.SilentLogger{}

	t.Run("nil overrides returns clone of resolved", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "v"}}
		got := applyAttributeOverrides(resolved, nil, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v", got, resolved)
		}
		// Mutating the returned map must not affect resolved.
		got["cli"].(map[string]any)["k"] = "mutated"
		if resolved["cli"].(map[string]any)["k"] != "v" {
			t.Fatalf("mutating the returned map affected resolved: %#v", resolved)
		}
	})

	t.Run("by_executor merged on top of resolved", func(t *testing.T) {
		resolved := map[string]any{
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
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "any-node", logger)
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
		resolved := map[string]any{
			"cli": map[string]any{"trace_to": "/resolved"},
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
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
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
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_executor": map[string]any{
				"http-node": map[string]any{
					"cli": map[string]any{"k": "should-not-apply"},
				},
			},
		}
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "any", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v", got, resolved)
		}
	})

	t.Run("by_node entry for a different node is ignored", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_node": map[string]any{
				"reference-pass": map[string]any{
					"cli": map[string]any{"k": "should-not-apply"},
				},
			},
		}
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v", got, resolved)
		}
	})

	t.Run("nil resolved + by_executor still produces merged result", func(t *testing.T) {
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{"synthetic_scenario": "exit-clean"},
				},
			},
		}
		got := applyAttributeOverrides(nil, ov, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{"synthetic_scenario": "exit-clean"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("malformed by_executor.<exec> fragment falls back to resolved", func(t *testing.T) {
		// lookupFragment requires the (key, subkey) lookup to land on a
		// map[string]any; non-object fragment values produce a (nil,
		// false) miss so the merge is skipped entirely.
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": "non-object",
			},
		}
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v (expected resolved when by_executor.<exec> fragment is malformed)", got, resolved)
		}
	})

	t.Run("malformed by_node.<node> fragment falls back to resolved", func(t *testing.T) {
		// Mirror of the by_executor.<exec> guard: a non-object value at
		// by_node.<node> must produce a (nil, false) miss so the merge
		// is skipped entirely.
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_node": map[string]any{
				"area-pass": []any{"not", "an", "object"},
			},
		}
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v (expected resolved when by_node.<node> fragment is malformed)", got, resolved)
		}
	})

	t.Run("malformed by_executor (top-level non-map) falls back to resolved", func(t *testing.T) {
		// lookupFragment's first guard: overrides["by_executor"] must
		// itself be a map. A non-object top-level by_executor (string,
		// list, scalar) produces a (nil, false) miss.
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_executor": "claude-agent=ignored",
		}
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v (expected resolved when by_executor itself is non-object)", got, resolved)
		}
	})

	t.Run("malformed by_node (top-level non-map) falls back to resolved", func(t *testing.T) {
		// Mirror of by_executor top-level guard.
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_node": float64(42),
		}
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v (expected resolved when by_node itself is non-object)", got, resolved)
		}
	})

	t.Run("nil resolved + overrides non-empty but no fragment matches → no Warn", func(t *testing.T) {
		capLog := shared.NewCapturingLogger()
		ov := map[string]any{
			"by_executor": map[string]any{
				"other-exec": map[string]any{
					"cli": map[string]any{"k": "should-not-apply"},
				},
			},
		}
		got := applyAttributeOverrides(nil, ov, "claude-agent", "area-pass", capLog)
		if len(got) != 0 {
			t.Fatalf("got %#v want empty map", got)
		}
		for _, rec := range capLog.Records() {
			if rec.Level == "warn" {
				t.Fatalf("expected no Warn log, got: %+v", rec)
			}
		}
	})

	t.Run("resolved is not mutated by merge", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{"k": "ov"},
				},
			},
		}
		_ = applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
		cli, ok := resolved["cli"].(map[string]any)
		if !ok || cli["k"] != "resolved" {
			t.Fatalf("resolved mutated by applyAttributeOverrides: %#v", resolved)
		}
	})

	t.Run("nested objects deep-merge across L3 + L4", func(t *testing.T) {
		resolved := map[string]any{
			"cli": map[string]any{
				"limits": map[string]any{
					"max_corrections": float64(3),
					"max_tokens":      float64(1000),
				},
			},
		}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{
						"limits": map[string]any{
							"max_corrections": float64(5),
						},
					},
				},
			},
			"by_node": map[string]any{
				"area-pass": map[string]any{
					"cli": map[string]any{
						"limits": map[string]any{
							"max_tokens": float64(3000),
						},
					},
				},
			},
		}
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
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
		resolved := map[string]any{
			"cli": map[string]any{
				"allowed_tools": []any{"shell", "search"},
			},
		}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{
						"allowed_tools": []any{"write"},
					},
				},
			},
		}
		got := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", logger)
		want := map[string]any{
			"cli": map[string]any{
				"allowed_tools": []any{"write"},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})
}
