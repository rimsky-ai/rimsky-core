// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type failingMatchEventPersist struct {
	err error
}

func (f failingMatchEventPersist) Transaction(_ context.Context, _ func(ctx context.Context, tx persistence.Tx) error) error {
	return f.err
}

func (f failingMatchEventPersist) Events() persistence.EventTable { return nil }

type recordingEventTable struct {
	persistence.EventTable
	appended []persistence.EventAppendInput
}

func (r *recordingEventTable) Append(_ context.Context, in persistence.EventAppendInput, _ persistence.Tx) error {
	r.appended = append(r.appended, in)
	return nil
}

type recordingMatchEventPersist struct {
	events *recordingEventTable
}

func (r recordingMatchEventPersist) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, nil)
}

func (r recordingMatchEventPersist) Events() persistence.EventTable { return r.events }

func matchedIndexes(matched []attributeOverrideMatch) []int {
	if len(matched) == 0 {
		return nil
	}
	out := make([]int, len(matched))
	for i, m := range matched {
		out[i] = m.Index
	}
	return out
}

func TestEmitOverrideMatchEventsAfterMerge_MatchedIndicesEachAppendOneEvent(t *testing.T) {
	instanceID := shared.UUID(uuid.New())
	recorder := &recordingEventTable{}

	err := emitOverrideMatchEventsAfterMerge(context.Background(), recordingMatchEventPersist{events: recorder}, shared.SilentLogger{}, instanceID, "area-pass",
		[]attributeOverrideMatch{{Index: 2, Fields: []string{"model", "prompt"}}, {Index: 0, Fields: []string{"budget"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recorder.appended) != 2 {
		t.Fatalf("expected 2 appended events, got %d", len(recorder.appended))
	}
	wantIndices := []int{2, 0}
	wantFields := [][]any{{"model", "prompt"}, {"budget"}}
	for i, in := range recorder.appended {
		if in.Kind != events.KindAttributeOverrideMatched() {
			t.Fatalf("event %d Kind = %v, want %v", i, in.Kind, events.KindAttributeOverrideMatched())
		}
		if in.InstanceID == nil || *in.InstanceID != instanceID {
			t.Fatalf("event %d InstanceID = %v, want %v", i, in.InstanceID, instanceID)
		}
		if got := in.Payload.Map()["override_index"]; got != float64(wantIndices[i]) {
			t.Fatalf("event %d payload[override_index] = %v, want %v", i, got, wantIndices[i])
		}
		if got := in.Payload.Map()["node_type"]; got != "area-pass" {
			t.Fatalf("event %d payload[node_type] = %v, want %q", i, got, "area-pass")
		}
		if got := in.Payload.Map()["fields"]; !reflect.DeepEqual(got, wantFields[i]) {
			t.Fatalf("event %d payload[fields] = %#v, want %#v", i, got, wantFields[i])
		}
	}
}

func TestApplyAttributeOverrides_MatchCarriesTheOverlaidFieldNames(t *testing.T) {
	ov := map[string]any{
		"by_match": []any{
			map[string]any{
				"matcher": map[string]any{"node_type": "fix"},
				"overlay": map[string]any{"model": "opus", "budget": 3},
			},
		},
	}
	_, matched := applyAttributeOverrides(map[string]any{}, ov, "e", "fix", "main", "", shared.SilentLogger{})
	if len(matched) != 1 {
		t.Fatalf("expected exactly one match, got %#v", matched)
	}
	if !reflect.DeepEqual(matched[0].Fields, []string{"budget", "model"}) {
		t.Fatalf("match fields = %#v, want the overlay's field names sorted", matched[0].Fields)
	}
}

func TestApplyAttributeOverrides(t *testing.T) {
	logger := shared.SilentLogger{}

	t.Run("nil overrides returns clone of resolved", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "v"}}
		got, _ := applyAttributeOverrides(resolved, nil, "claude-agent", "area-pass", "main", "", logger)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v", got, resolved)
		}
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
				"trace_to":           "/by-node",
				"synthetic_scenario": "exit-clean",
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

	t.Run("malformed by_executor.<exec> fragment falls back to resolved and warns", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": "non-object",
			},
		}
		capLog := shared.NewCapturingLogger()
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", capLog)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v (expected resolved when by_executor.<exec> fragment is malformed)", got, resolved)
		}
		assertHasWarn(t, capLog)
	})

	t.Run("malformed by_node.<node> fragment falls back to resolved and warns", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_node": map[string]any{
				"area-pass": []any{"not", "an", "object"},
			},
		}
		capLog := shared.NewCapturingLogger()
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", capLog)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v (expected resolved when by_node.<node> fragment is malformed)", got, resolved)
		}
		assertHasWarn(t, capLog)
	})

	t.Run("malformed by_executor (top-level non-map) falls back to resolved and warns", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_executor": "claude-agent=ignored",
		}
		capLog := shared.NewCapturingLogger()
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", capLog)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v (expected resolved when by_executor itself is non-object)", got, resolved)
		}
		assertHasWarn(t, capLog)
	})

	t.Run("malformed by_node (top-level non-map) falls back to resolved and warns", func(t *testing.T) {
		resolved := map[string]any{"cli": map[string]any{"k": "resolved"}}
		ov := map[string]any{
			"by_node": float64(42),
		}
		capLog := shared.NewCapturingLogger()
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", capLog)
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("got %#v want %#v (expected resolved when by_node itself is non-object)", got, resolved)
		}
		assertHasWarn(t, capLog)
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
					"max_corrections": float64(5),
					"max_tokens":      float64(3000),
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

	t.Run("directive-shaped override and default values are never substituted", func(t *testing.T) {
		resolved := map[string]any{
			"cli": map[string]any{"prompt": "{{nodes.upstream.attribute.result}}"},
		}
		ov := map[string]any{
			"by_executor": map[string]any{
				"claude-agent": map[string]any{
					"cli": map[string]any{
						"prompt":  "{{nodes.upstream.attribute.result}}",
						"trailer": "{{params.foo | fallback}}",
					},
				},
			},
			"by_node": map[string]any{
				"area-pass": map[string]any{
					"cli": map[string]any{"note": "{{claim.data.field}}"},
				},
			},
		}
		got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
		want := map[string]any{
			"cli": map[string]any{
				"prompt":  "{{nodes.upstream.attribute.result}}",
				"trailer": "{{params.foo | fallback}}",
				"note":    "{{claim.data.field}}",
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("a directive-shaped override/default value must pass through as a literal string, unsubstituted: got %#v want %#v", got, want)
		}
	})
}

func TestEmitOverrideMatchEventsAfterMerge_TransactionFailurePropagates(t *testing.T) {
	wantErr := errors.New("boom")
	capLog := shared.NewCapturingLogger()
	instanceID := shared.UUID(uuid.New())

	err := emitOverrideMatchEventsAfterMerge(context.Background(), failingMatchEventPersist{err: wantErr}, capLog, instanceID, "area-pass", []attributeOverrideMatch{{Index: 0}})
	if err == nil {
		t.Fatal("expected error to propagate when the match-event transaction fails")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped %v, got %v", wantErr, err)
	}
	assertHasWarn(t, capLog)
}

func TestEmitOverrideMatchEventsAfterMerge_NoMatchesIsNoop(t *testing.T) {
	instanceID := shared.UUID(uuid.New())
	err := emitOverrideMatchEventsAfterMerge(context.Background(), failingMatchEventPersist{err: errors.New("must not be called")}, shared.SilentLogger{}, instanceID, "area-pass", nil)
	if err != nil {
		t.Fatalf("expected no-op for empty matched indices, got %v", err)
	}
}

func assertHasWarn(t *testing.T, capLog *shared.CapturingLogger) {
	t.Helper()
	for _, rec := range capLog.Records() {
		if rec.Level == "warn" {
			return
		}
	}
	t.Fatalf("expected a Warn log, got records=%#v", capLog.Records())
}

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
		if !reflect.DeepEqual(matchedIndexes(matched), []int{0}) {
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
		got, matched := applyAttributeOverrides(resolved, ov, "e", "fix", "main", "", logger)
		if got["x"] != "applied" || !reflect.DeepEqual(matchedIndexes(matched), []int{0}) {
			t.Fatalf("got=%#v matched=%#v want overlay applied", got, matched)
		}
		got2, matched2 := applyAttributeOverrides(resolved, ov, "e", "other", "main", "", logger)
		if _, exists := got2["x"]; exists || len(matched2) != 0 {
			t.Fatalf("got=%#v matched=%#v want overlay not applied", got2, matched2)
		}
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
		got1, matched1 := applyAttributeOverrides(resolved, ov, "exec-a", "node-a", "main", "", logger)
		if got1["flag"] != true || !reflect.DeepEqual(matchedIndexes(matched1), []int{0}) {
			t.Fatalf("dispatch A: got=%#v matched=%#v", got1, matched1)
		}
		got2, matched2 := applyAttributeOverrides(resolved, ov, "exec-b", "node-b", "worker", "k1", logger)
		if got2["flag"] != true || !reflect.DeepEqual(matchedIndexes(matched2), []int{0}) {
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
		if !reflect.DeepEqual(matchedIndexes(matched), []int{0, 1}) {
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
		got1, matched1 := applyAttributeOverrides(map[string]any{}, ov, "e", "n", "main", "k1", logger)
		if got1["tag"] != "for-k1" || !reflect.DeepEqual(matchedIndexes(matched1), []int{0}) {
			t.Fatalf("child_key=k1: got=%#v matched=%#v", got1, matched1)
		}
		got2, matched2 := applyAttributeOverrides(map[string]any{}, ov, "e", "n", "main", "k2", logger)
		if _, exists := got2["tag"]; exists || len(matched2) != 0 {
			t.Fatalf("child_key=k2: got=%#v matched=%#v", got2, matched2)
		}
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
		if got1["where"] != "main" || !reflect.DeepEqual(matchedIndexes(matched1), []int{0}) {
			t.Fatalf("graph=main: got=%#v matched=%#v", got1, matched1)
		}
		got2, matched2 := applyAttributeOverrides(map[string]any{}, ov, "e", "n", "worker", "", logger)
		if got2["where"] != "worker" || !reflect.DeepEqual(matchedIndexes(matched2), []int{1}) {
			t.Fatalf("graph=worker: got=%#v matched=%#v", got2, matched2)
		}
	})

	t.Run("attrs.<path> equality on primitives", func(t *testing.T) {
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
		if got["hit"] != "yes" || !reflect.DeepEqual(matchedIndexes(matched), []int{0}) {
			t.Fatalf("all primitives matched: got=%#v matched=%#v", got, matched)
		}
		resolved2 := map[string]any{
			"cli": map[string]any{"iter": float64(2), "allow_html": true},
		}
		_, matched2 := applyAttributeOverrides(resolved2, ov, "e", "n", "main", "", logger)
		if len(matched2) != 0 {
			t.Fatalf("missing model: matched=%#v", matched2)
		}
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
		if got["hit"] != "yes" || !reflect.DeepEqual(matchedIndexes(matched), []int{0}) {
			t.Fatalf("L3 visible to L5 matcher: got=%#v matched=%#v", got, matched)
		}
	})

	t.Run("matcher reads from post-L4 snapshot, not running L5", func(t *testing.T) {
		resolved := map[string]any{}
		ov := map[string]any{
			"by_match": []any{
				map[string]any{
					"matcher": map[string]any{},
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
		if !reflect.DeepEqual(matchedIndexes(matched), []int{0}) {
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
		bm, _ := ov["by_match"].([]any)
		entry0, _ := bm[0].(map[string]any)
		overlay0, _ := entry0["overlay"].(map[string]any)
		if overlay0["k"] != "overlay" {
			t.Fatalf("overrides mutated: %#v", ov)
		}
	})

	t.Run("matcher with unknown keys is skipped (defense-in-depth)", func(t *testing.T) {
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
		if !reflect.DeepEqual(matchedIndexes(matched), []int{1}) {
			t.Fatalf("matched must contain only the valid index 1: got=%#v", matched)
		}
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
		capLog := shared.NewCapturingLogger()
		resolved := map[string]any{}
		ov := map[string]any{
			"by_match": []any{
				"not-an-object",
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
		if !reflect.DeepEqual(matchedIndexes(matched), []int{1}) {
			t.Fatalf("matched must contain only the valid index 1 (preserves original index): got=%#v", matched)
		}
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
