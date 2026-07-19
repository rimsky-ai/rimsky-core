// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package matcher

import (
	"encoding/json"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestEvaluate(t *testing.T) {
	silent := shared.SilentLogger{}

	t.Run("empty matcher fires on every dispatch", func(t *testing.T) {
		ctx := Context{Executor: "x", NodeType: "n", Graph: "main", ChildKey: "", AttributeBag: nil}
		if !Evaluate(Matcher{}, ctx, silent, 0) {
			t.Fatal("empty matcher must match every dispatch")
		}
	})

	t.Run("node_type happy path and mismatch", func(t *testing.T) {
		ctx := Context{NodeType: "area-pass"}
		if !Evaluate(Matcher{"node_type": "area-pass"}, ctx, silent, 0) {
			t.Fatal("node_type match should fire")
		}
		if Evaluate(Matcher{"node_type": "other"}, ctx, silent, 0) {
			t.Fatal("node_type mismatch should not fire")
		}
	})

	t.Run("executor happy path and mismatch", func(t *testing.T) {
		ctx := Context{Executor: "claude-agent"}
		if !Evaluate(Matcher{"executor": "claude-agent"}, ctx, silent, 0) {
			t.Fatal("executor match should fire")
		}
		if Evaluate(Matcher{"executor": "other"}, ctx, silent, 0) {
			t.Fatal("executor mismatch should not fire")
		}
	})

	t.Run("graph happy path and mismatch", func(t *testing.T) {
		ctx := Context{Graph: "main"}
		if !Evaluate(Matcher{"graph": "main"}, ctx, silent, 0) {
			t.Fatal("graph match should fire")
		}
		if Evaluate(Matcher{"graph": "sub"}, ctx, silent, 0) {
			t.Fatal("graph mismatch should not fire")
		}
	})

	t.Run("child_key happy path and mismatch", func(t *testing.T) {
		ctx := Context{ChildKey: "partition-a"}
		if !Evaluate(Matcher{"child_key": "partition-a"}, ctx, silent, 0) {
			t.Fatal("child_key match should fire")
		}
		if Evaluate(Matcher{"child_key": "partition-b"}, ctx, silent, 0) {
			t.Fatal("child_key mismatch should not fire")
		}
	})

	t.Run("attrs happy path with nested map walk", func(t *testing.T) {
		ctx := Context{AttributeBag: map[string]any{
			"cli": map[string]any{
				"profile": "fast",
				"limits":  map[string]any{"silence_timeout_ms": float64(60000)},
			},
		}}
		if !Evaluate(Matcher{"attrs": map[string]any{"cli.profile": "fast"}}, ctx, silent, 0) {
			t.Fatal("attrs.cli.profile match should fire")
		}
		if !Evaluate(Matcher{"attrs": map[string]any{"cli.limits.silence_timeout_ms": float64(60000)}}, ctx, silent, 0) {
			t.Fatal("attrs.cli.limits.silence_timeout_ms match should fire")
		}
	})

	t.Run("attrs mismatch on missing path", func(t *testing.T) {
		ctx := Context{AttributeBag: map[string]any{"cli": map[string]any{"profile": "fast"}}}
		if Evaluate(Matcher{"attrs": map[string]any{"cli.missing": "x"}}, ctx, silent, 0) {
			t.Fatal("missing-path matcher should not fire")
		}
		if Evaluate(Matcher{"attrs": map[string]any{"cli.profile.unreachable": "x"}}, ctx, silent, 0) {
			t.Fatal("walk through non-map intermediate should not fire")
		}
	})

	t.Run("AND across multiple keys", func(t *testing.T) {
		ctx := Context{NodeType: "n", Executor: "x", Graph: "main"}
		if !Evaluate(Matcher{"node_type": "n", "executor": "x", "graph": "main"}, ctx, silent, 0) {
			t.Fatal("all-keys-match should fire")
		}
		if Evaluate(Matcher{"node_type": "n", "executor": "x", "graph": "sub"}, ctx, silent, 0) {
			t.Fatal("one-key-mismatch should not fire")
		}
	})

	t.Run("unknown key causes defensive skip with Warn log", func(t *testing.T) {
		cap := shared.NewCapturingLogger()
		ctx := Context{NodeType: "n"}
		if Evaluate(Matcher{"bogus_key": "x"}, ctx, cap, 7) {
			t.Fatal("matcher with unknown key must not fire")
		}
		records := cap.Records()
		if len(records) == 0 {
			t.Fatal("expected a Warn log record for unknown key")
		}
		var saw bool
		for _, r := range records {
			if r.Level == "warn" && r.Fields["unknown_key"] == "bogus_key" && r.Fields["entry_index"] == 7 {
				saw = true
				break
			}
		}
		if !saw {
			t.Fatalf("expected Warn with unknown_key=bogus_key, entry_index=7; got %+v", records)
		}
	})

	t.Run("nil logger does not panic on unknown key skip", func(t *testing.T) {
		if Evaluate(Matcher{"bogus_key": "x"}, Context{}, nil, 0) {
			t.Fatal("matcher with unknown key must not fire")
		}
	})

	t.Run("primitive equality coerces across number kinds", func(t *testing.T) {
		ctx := Context{AttributeBag: map[string]any{"n": int(42)}}
		if !Evaluate(Matcher{"attrs": map[string]any{"n": float64(42)}}, ctx, silent, 0) {
			t.Fatal("int(42) should equal float64(42)")
		}

		ctx = Context{AttributeBag: map[string]any{"n": int64(42)}}
		if !Evaluate(Matcher{"attrs": map[string]any{"n": float64(42)}}, ctx, silent, 0) {
			t.Fatal("int64(42) should equal float64(42)")
		}

		ctx = Context{AttributeBag: map[string]any{"n": float64(42)}}
		if !Evaluate(Matcher{"attrs": map[string]any{"n": int(42)}}, ctx, silent, 0) {
			t.Fatal("float64(42) should equal int(42)")
		}

		ctx = Context{AttributeBag: map[string]any{"n": json.Number("42")}}
		if !Evaluate(Matcher{"attrs": map[string]any{"n": float64(42)}}, ctx, silent, 0) {
			t.Fatal("json.Number(42) should equal float64(42)")
		}

		ctx = Context{AttributeBag: map[string]any{"n": float64(42)}}
		if !Evaluate(Matcher{"attrs": map[string]any{"n": json.Number("42")}}, ctx, silent, 0) {
			t.Fatal("float64(42) should equal json.Number(42)")
		}
	})

	t.Run("primitive equality rejects across types", func(t *testing.T) {
		ctx := Context{AttributeBag: map[string]any{"v": "42"}}
		if Evaluate(Matcher{"attrs": map[string]any{"v": float64(42)}}, ctx, silent, 0) {
			t.Fatal("string \"42\" should not equal float64(42)")
		}
		ctx = Context{AttributeBag: map[string]any{"v": true}}
		if Evaluate(Matcher{"attrs": map[string]any{"v": "true"}}, ctx, silent, 0) {
			t.Fatal("bool true should not equal string \"true\"")
		}
	})
}

func TestWalkAttrPath(t *testing.T) {
	bag := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "leaf",
			},
		},
	}

	got, found := walkAttrPath(bag, "a.b.c")
	if !found || got != "leaf" {
		t.Fatalf("walkAttrPath(a.b.c) = (%v, %v); want (leaf, true)", got, found)
	}

	_, found = walkAttrPath(bag, "a.missing")
	if found {
		t.Fatal("missing path must report found=false")
	}

	_, found = walkAttrPath(bag, "a.b.c.deeper")
	if found {
		t.Fatal("walk through non-map intermediate must report found=false")
	}

	got, found = walkAttrPath(bag, "a")
	if !found {
		t.Fatal("top-level lookup should resolve")
	}
	if _, ok := got.(map[string]any); !ok {
		t.Fatalf("expected map at top-level resolution; got %T", got)
	}
}

func TestPrimitiveEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"strings equal", "foo", "foo", true},
		{"strings unequal", "foo", "bar", false},
		{"bool equal", true, true, true},
		{"bool unequal", true, false, false},
		{"int vs int", int(1), int(1), true},
		{"int vs int64", int(1), int64(1), true},
		{"int vs float64", int(1), float64(1), true},
		{"int64 vs int", int64(1), int(1), true},
		{"int64 vs int64", int64(1), int64(1), true},
		{"int64 vs float64", int64(1), float64(1), true},
		{"float64 vs int", float64(1), int(1), true},
		{"float64 vs int64", float64(1), int64(1), true},
		{"float64 vs float64", float64(1), float64(1), true},
		{"json.Number vs float64", json.Number("1"), float64(1), true},
		{"float64 vs json.Number", float64(1), json.Number("1"), true},
		{"string vs float64 rejected", "1", float64(1), false},
		{"non-primitive a", map[string]any{}, "x", false},
		{"non-primitive b", "x", map[string]any{}, false},
		{"equal maps rejected as non-primitive", map[string]any{"x": 1}, map[string]any{"x": 1}, false},
		{"equal slices rejected as non-primitive", []any{1, 2}, []any{1, 2}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := primitiveEqual(c.a, c.b); got != c.want {
				t.Fatalf("primitiveEqual(%v, %v) = %v; want %v", c.a, c.b, got, c.want)
			}
		})
	}
}
