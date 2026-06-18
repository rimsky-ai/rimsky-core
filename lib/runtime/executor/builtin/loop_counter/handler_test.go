// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package loop_counter

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

func TestExecute_TagsAndDeltaAcrossBoundary(t *testing.T) {
	cases := []struct {
		name     string
		max      int
		count    int
		wantTag  string
		wantNew  float64
	}{
		{name: "first_dispatch_no_count", max: 3, count: -1, wantTag: "loop", wantNew: 1},
		{name: "below_max", max: 3, count: 1, wantTag: "loop", wantNew: 2},
		{name: "reaches_max", max: 3, count: 2, wantTag: "done", wantNew: 3},
		{name: "max_of_one", max: 1, count: -1, wantTag: "done", wantNew: 1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			attrs := map[string]any{"max": float64(tc.max)}
			if tc.count >= 0 {
				attrs["count"] = float64(tc.count)
			}
			outcome := mustExecute(t, attrs)
			success := outcome.GetSuccess()
			if success == nil {
				t.Fatalf("expected Success outcome, got %T", outcome.GetOutcome())
			}
			if !success.GetChanged() {
				t.Errorf("Success.Changed = false, want true")
			}
			tags := success.GetTags()
			if len(tags) != 1 || tags[0] != tc.wantTag {
				t.Errorf("Success.Tags = %v, want [%q]", tags, tc.wantTag)
			}
			delta := success.GetAttributesDelta().AsMap()
			gotCount, ok := delta["count"].(float64)
			if !ok {
				t.Fatalf("AttributesDelta.count missing or non-numeric: %#v", delta["count"])
			}
			if gotCount != tc.wantNew {
				t.Errorf("AttributesDelta.count = %v, want %v", gotCount, tc.wantNew)
			}
		})
	}
}

func TestExecute_SchemaViolationsReturnError(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]any
	}{
		{name: "missing_max", attrs: map[string]any{}},
		{name: "max_zero", attrs: map[string]any{"max": float64(0)}},
		{name: "max_negative", attrs: map[string]any{"max": float64(-1)}},
		{name: "max_non_numeric", attrs: map[string]any{"max": "three"}},
		{name: "count_non_numeric", attrs: map[string]any{"max": float64(3), "count": "two"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			outcome := mustExecute(t, tc.attrs)
			errOut := outcome.GetError()
			if errOut == nil {
				t.Fatalf("expected Error outcome, got %T", outcome.GetOutcome())
			}
			if errOut.GetErrorClass() != "attributes_schema_invalid" {
				t.Errorf("Error.ErrorClass = %q, want %q",
					errOut.GetErrorClass(), "attributes_schema_invalid")
			}
		})
	}
}

func TestExecute_NilAttributes(t *testing.T) {
	t.Parallel()
	h := New()
	req := &genv1.ExecuteRequest{
		DispatchId: "00000000-0000-0000-0000-000000000001",
		NodeId:     "00000000-0000-0000-0000-000000000002",
	}
	outcome, err := h.Execute(context.Background(), req, executor.HandlerContext{})
	if err != nil {
		t.Fatalf("Execute returned non-nil error: %v", err)
	}
	if outcome.GetError() == nil {
		t.Fatalf("expected Error outcome, got %T", outcome.GetOutcome())
	}
}

func TestDeclaredTags(t *testing.T) {
	got := DeclaredTags()
	want := []string{"loop", "done"}
	if len(got) != len(want) {
		t.Fatalf("DeclaredTags() = %v, want %v", got, want)
	}
	gotSet := map[string]bool{}
	for _, tag := range got {
		gotSet[tag] = true
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Errorf("DeclaredTags() missing %q (got %v)", w, got)
		}
	}
}

func mustExecute(t *testing.T, attrs map[string]any) *genv1.Outcome {
	t.Helper()
	h := New()
	attrStruct, err := structpb.NewStruct(attrs)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	req := &genv1.ExecuteRequest{
		DispatchId: "00000000-0000-0000-0000-000000000001",
		NodeId:     "00000000-0000-0000-0000-000000000002",
		Attributes: attrStruct,
	}
	outcome, err := h.Execute(context.Background(), req, executor.HandlerContext{})
	if err != nil {
		t.Fatalf("Execute returned non-nil error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("Execute returned nil outcome with nil error")
	}
	return outcome
}
