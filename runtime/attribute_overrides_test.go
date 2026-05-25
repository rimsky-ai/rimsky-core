// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"reflect"
	"testing"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// TestApplyAttributeOverrides covers the L3 + L4 runtime merge layers.
// L1 (template defaults) folded into the effective schema at
// registration; L2 (per-node schema) lives inside the resolved bag
// already — both have happened before this function is called.
func TestApplyAttributeOverrides(t *testing.T) {
	logger := shared.SilentLogger{}

	t.Run("nil overrides returns clone of resolved", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "v"}}
		got, _ := applyAttributeOverrides(resolved, nil, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "any-node", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "any", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(nil, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(nil, ov, "claude-agent", "area-pass", "main", "", capLog)
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
		_, _ = applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
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

// TestApplyAttributeOverrides_ByMatch covers the L5 by_match merge
// layer added per .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md.
func TestApplyAttributeOverrides_ByMatch(t *testing.T) {
	logger := shared.SilentLogger{}

	t.Run("empty by_match is a no-op", func(t *testing.T) {
		resolved := map[string]any{"k": "v"}
		ov := map[string]any{"by_match": []any{}}
		got, matched := applyAttributeOverrides(resolved, ov, "e", "n", "main", "", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v", got, resolved)
		}
		if len(matched) != 0 {
			t.Fatalf("matched should be empty: %#v", matched)
		}
	})

	t.Run("single matcher node_type only", func(t *testing.T) {
		resolved := map[string]any{}
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{"node_type": "fix"},
					"overlay": map[string]any{"x": "applied"},
				},
			},
		}
		got, matched := applyAttributeOverrides(resolved, ov, "e", "fix", "main", "", logger)
		if got["x"] != "applied" {
			t.Fatalf("overlay not applied: %#v", got)
		}
		if !reflect.DeepEqual(matched, []int{0}) {
			t.Fatalf("matched = %#v", matched)
		}
		got2, matched2 := applyAttributeOverrides(resolved, ov, "e", "other", "main", "", logger)
		if _, exists := got2["x"]; exists {
			t.Fatalf("overlay incorrectly applied: %#v", got2)
		}
		if len(matched2) != 0 {
			t.Fatalf("matched2 should be empty: %#v", matched2)
		}
	})

	t.Run("multiple matcher keys AND together", func(t *testing.T) {
		// node_type AND attrs.iter_num=1 — both must match. The matcher's
		// "attrs" key carries dotted paths that walk into the dispatch
		// attribute bag (which IS the attrs bag).
		resolved := map[string]any{"iter_num": float64(1)}
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{
						"node_type": "fix",
						"attrs":     map[string]any{"iter_num": float64(1)},
					},
					"overlay": map[string]any{"x": "applied"},
				},
			},
		}
		// Both match → fires.
		got, matched := applyAttributeOverrides(resolved, ov, "e", "fix", "main", "", logger)
		if got["x"] != "applied" || !reflect.DeepEqual(matched, []int{0}) {
			t.Fatalf("got=%#v matched=%#v want overlay applied", got, matched)
		}
		// node_type wrong → no fire.
		got2, matched2 := applyAttributeOverrides(resolved, ov, "e", "other", "main", "", logger)
		if _, exists := got2["x"]; exists || len(matched2) != 0 {
			t.Fatalf("got=%#v matched=%#v want overlay not applied", got2, matched2)
		}
		// attrs.iter_num wrong → no fire.
		resolved3 := map[string]any{"iter_num": float64(2)}
		got3, matched3 := applyAttributeOverrides(resolved3, ov, "e", "fix", "main", "", logger)
		if _, exists := got3["x"]; exists || len(matched3) != 0 {
			t.Fatalf("got=%#v matched=%#v want overlay not applied", got3, matched3)
		}
	})

	t.Run("empty matcher matches every dispatch", func(t *testing.T) {
		resolved := map[string]any{}
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{},
					"overlay": map[string]any{"flag": true},
				},
			},
		}
		// Two arbitrary dispatch contexts — both should fire.
		got1, matched1 := applyAttributeOverrides(resolved, ov, "exec-a", "node-a", "main", "", logger)
		if got1["flag"] != true || !reflect.DeepEqual(matched1, []int{0}) {
			t.Fatalf("dispatch A: got=%#v matched=%#v", got1, matched1)
		}
		got2, matched2 := applyAttributeOverrides(resolved, ov, "exec-b", "node-b", "worker", "k1", logger)
		if got2["flag"] != true || !reflect.DeepEqual(matched2, []int{0}) {
			t.Fatalf("dispatch B: got=%#v matched=%#v", got2, matched2)
		}
	})

	t.Run("declaration order — later wins on conflict", func(t *testing.T) {
		resolved := map[string]any{}
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{"node_type": "n"},
					"overlay": map[string]any{
						"shared": "first",
						"first":  "only",
					},
				},
				map[string]any{
					"matcher": map[string]any{"node_type": "n"},
					"overlay": map[string]any{
						"shared": "second",
						"second": "only",
					},
				},
			},
		}
		got, matched := applyAttributeOverrides(resolved, ov, "e", "n", "main", "", logger)
		if got["shared"] != "second" {
			t.Fatalf("later overlay must win on conflict: got %#v", got)
		}
		if got["first"] != "only" || got["second"] != "only" {
			t.Fatalf("non-conflicting overlay paths must both apply: %#v", got)
		}
		if !reflect.DeepEqual(matched, []int{0, 1}) {
			t.Fatalf("matched = %#v want [0, 1]", matched)
		}
	})

	t.Run("child_key matching", func(t *testing.T) {
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{"child_key": "k1"},
					"overlay": map[string]any{"tag": "for-k1"},
				},
			},
		}
		// Specific child_key fires.
		got1, matched1 := applyAttributeOverrides(map[string]any{}, ov, "e", "n", "main", "k1", logger)
		if got1["tag"] != "for-k1" || !reflect.DeepEqual(matched1, []int{0}) {
			t.Fatalf("child_key=k1: got=%#v matched=%#v", got1, matched1)
		}
		// Different child_key does not fire.
		got2, matched2 := applyAttributeOverrides(map[string]any{}, ov, "e", "n", "main", "k2", logger)
		if _, exists := got2["tag"]; exists || len(matched2) != 0 {
			t.Fatalf("child_key=k2: got=%#v matched=%#v", got2, matched2)
		}
		// Empty child_key does not match a matcher specifying child_key.
		got3, matched3 := applyAttributeOverrides(map[string]any{}, ov, "e", "n", "main", "", logger)
		if _, exists := got3["tag"]; exists || len(matched3) != 0 {
			t.Fatalf("child_key=empty: got=%#v matched=%#v", got3, matched3)
		}
	})

	t.Run("graph matching", func(t *testing.T) {
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{"graph": "main"},
					"overlay": map[string]any{"where": "main"},
				},
				map[string]any{
					"matcher": map[string]any{"graph": "worker"},
					"overlay": map[string]any{"where": "worker"},
				},
			},
		}
		got1, matched1 := applyAttributeOverrides(map[string]any{}, ov, "e", "n", "main", "", logger)
		if got1["where"] != "main" || !reflect.DeepEqual(matched1, []int{0}) {
			t.Fatalf("graph=main: got=%#v matched=%#v", got1, matched1)
		}
		got2, matched2 := applyAttributeOverrides(map[string]any{}, ov, "e", "n", "worker", "", logger)
		if got2["where"] != "worker" || !reflect.DeepEqual(matched2, []int{1}) {
			t.Fatalf("graph=worker: got=%#v matched=%#v", got2, matched2)
		}
	})

	t.Run("attrs.<path> equality on primitives", func(t *testing.T) {
		// String, number, and bool primitives all match.
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{
						"attrs": map[string]any{
							"cli.model":      "gpt",
							"cli.iter":       float64(2),
							"cli.allow_html": true,
						},
					},
					"overlay": map[string]any{"hit": "yes"},
				},
			},
		}
		resolved := map[string]any{
			"cli": map[string]any{
				"model":      "gpt",
				"iter":       float64(2),
				"allow_html": true,
			},
		}
		got, matched := applyAttributeOverrides(resolved, ov, "e", "n", "main", "", logger)
		if got["hit"] != "yes" || !reflect.DeepEqual(matched, []int{0}) {
			t.Fatalf("all primitives matched: got=%#v matched=%#v", got, matched)
		}
		// Missing path → no match.
		resolved2 := map[string]any{
			"cli": map[string]any{"iter": float64(2), "allow_html": true},
		}
		_, matched2 := applyAttributeOverrides(resolved2, ov, "e", "n", "main", "", logger)
		if len(matched2) != 0 {
			t.Fatalf("missing model: matched=%#v", matched2)
		}
		// Non-primitive value at path → no match.
		resolved3 := map[string]any{
			"cli": map[string]any{
				"model":      map[string]any{"nested": "obj"},
				"iter":       float64(2),
				"allow_html": true,
			},
		}
		_, matched3 := applyAttributeOverrides(resolved3, ov, "e", "n", "main", "", logger)
		if len(matched3) != 0 {
			t.Fatalf("non-primitive model: matched=%#v", matched3)
		}
	})

	t.Run("matcher reads from post-L4 bag", func(t *testing.T) {
		// L3 sets iter_num=1 in the dispatch bag; matcher
		// attrs: {iter_num: 1} fires (matcher reads bag.iter_num).
		resolved := map[string]any{}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{"iter_num": float64(1)},
			},
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{
						"attrs": map[string]any{"iter_num": float64(1)},
					},
					"overlay": map[string]any{"hit": "yes"},
				},
			},
		}
		got, matched := applyAttributeOverrides(resolved, ov, "claude-agent", "n", "main", "", logger)
		if got["hit"] != "yes" || !reflect.DeepEqual(matched, []int{0}) {
			t.Fatalf("L3 visible to L5 matcher: got=%#v matched=%#v", got, matched)
		}
	})

	t.Run("matcher reads from post-L4 snapshot, not running L5", func(t *testing.T) {
		// First L5 entry sets flag=true in the bag; second L5 entry's
		// matcher requires flag=true — it must NOT fire because the
		// matcher snapshot is taken before any L5 overlay applies.
		resolved := map[string]any{}
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{}, // matches every dispatch
					"overlay": map[string]any{"flag": true},
				},
				map[string]any{
					"matcher": map[string]any{
						"attrs": map[string]any{"flag": true},
					},
					"overlay": map[string]any{"hit": "second"},
				},
			},
		}
		got, matched := applyAttributeOverrides(resolved, ov, "e", "n", "main", "", logger)
		if _, ok := got["hit"]; ok {
			t.Fatalf("second L5 entry must NOT fire (matcher reads pre-L5 snapshot): %#v", got)
		}
		if !reflect.DeepEqual(matched, []int{0}) {
			t.Fatalf("only first L5 entry should match: %#v", matched)
		}
	})

	t.Run("non-mutation invariant", func(t *testing.T) {
		resolved := map[string]any{"k": "resolved"}
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{},
					"overlay": map[string]any{"k": "overlay"},
				},
			},
		}
		_, _ = applyAttributeOverrides(resolved, ov, "e", "n", "main", "", logger)
		if resolved["k"] != "resolved" {
			t.Fatalf("resolved mutated: %#v", resolved)
		}
		// Overrides should not have been mutated either.
		bm, _ := ov["by_match"].([]any)
		entry0, _ := bm[0].(map[string]any)
		overlay0, _ := entry0["overlay"].(map[string]any)
		if overlay0["k"] != "overlay" {
			t.Fatalf("overrides mutated: %#v", ov)
		}
	})

	t.Run("matcher with unknown keys is skipped (defense-in-depth)", func(t *testing.T) {
		// Out-of-band corruption: the validator at instance-create
		// rejects unknown matcher keys, but persistence drift could
		// surface them at runtime. A matcher whose ONLY key is an
		// unknown one would have len > 0 (so the empty-matcher
		// wildcard branch is bypassed) but skip every recognised
		// branch — without the closed-key guard it would silently
		// match every dispatch. Per the inertness discipline, only
		// the routing structure is read here, so the warn names the
		// offending entry index + key only.
		capLog := shared.NewCapturingLogger()
		resolved := map[string]any{}
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{"bogus_key": "x"},
					"overlay": map[string]any{"should_not_apply": "true"},
				},
				map[string]any{
					"matcher": map[string]any{"node_type": "fix"},
					"overlay": map[string]any{"valid_hit": "yes"},
				},
			},
		}
		got, matched := applyAttributeOverrides(resolved, ov, "e", "fix", "main", "", capLog)
		if _, present := got["should_not_apply"]; present {
			t.Fatalf("matcher with unknown key fired: got=%#v", got)
		}
		if got["valid_hit"] != "yes" {
			t.Fatalf("valid entry must still fire: got=%#v", got)
		}
		if !reflect.DeepEqual(matched, []int{1}) {
			t.Fatalf("matched must contain only the valid index 1: got=%#v", matched)
		}
		// Confirm the warn record carries the offending entry index +
		// unknown key.
		var sawWarn bool
		for _, rec := range capLog.Records() {
			if rec.Level != "warn" {
				continue
			}
			idx, hasIdx := rec.Fields["entry_index"]
			key, hasKey := rec.Fields["unknown_key"]
			if hasIdx && hasKey && idx == 0 && key == "bogus_key" {
				sawWarn = true
				break
			}
		}
		if !sawWarn {
			t.Fatalf("expected warn log naming entry_index=0 + unknown_key=bogus_key; got records=%#v", capLog.Records())
		}
	})

	t.Run("malformed by_match entry is skipped per-entry, valid entries still fire", func(t *testing.T) {
		// Out-of-band data corruption: by_match[0] is a string (not an
		// object). The valid entry at index 1 must still fire, and the
		// malformed slot must produce a warn log with the offending
		// index. The prior all-or-nothing behaviour (lookupMatchList
		// returning false on any per-entry shape mismatch) hid the
		// corruption — every entry's counter stayed at 0 even when only
		// one slot was broken.
		capLog := shared.NewCapturingLogger()
		resolved := map[string]any{}
		ov := map[string]any{
			"by_match": []any{
				"not-an-object", // malformed
				map[string]any{
					"matcher": map[string]any{"node_type": "fix"},
					"overlay": map[string]any{"hit": "yes"},
				},
			},
		}
		got, matched := applyAttributeOverrides(resolved, ov, "e", "fix", "main", "", capLog)
		if got["hit"] != "yes" {
			t.Fatalf("valid entry must still fire: got=%#v", got)
		}
		if !reflect.DeepEqual(matched, []int{1}) {
			t.Fatalf("matched must contain only the valid index 1 (preserves original index): got=%#v", matched)
		}
		// Find the warn record naming the offending entry index.
		var sawWarn bool
		for _, rec := range capLog.Records() {
			if rec.Level != "warn" {
				continue
			}
			if idx, ok := rec.Fields["entry_index"]; ok && idx == 0 {
				sawWarn = true
				break
			}
		}
		if !sawWarn {
			t.Fatalf("expected warn log identifying entry_index=0; got records=%#v", capLog.Records())
		}
	})
}
