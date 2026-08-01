// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attribute_passthrough

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

func TestExecute_EmptyInputsProduceEmptyDelta(t *testing.T) {
	t.Parallel()
	outcome := mustExecute(t, map[string]any{})
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success outcome, got %T", outcome.GetOutcome())
	}
	if success.GetChanged() {
		t.Errorf("Success.Changed = true for empty inputs, want false")
	}
	delta := success.GetAttributesDelta().AsMap()
	if len(delta) != 0 {
		t.Errorf("AttributesDelta = %v, want empty map", delta)
	}
}

func TestExecute_SingleInputEchoedToOutput(t *testing.T) {
	t.Parallel()
	in := map[string]any{"processed_value": float64(7)}
	outcome := mustExecute(t, in)
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success outcome, got %T", outcome.GetOutcome())
	}
	if !success.GetChanged() {
		t.Errorf("Success.Changed = false for non-empty input, want true")
	}
	delta := success.GetAttributesDelta().AsMap()
	if !reflect.DeepEqual(delta, in) {
		t.Errorf("AttributesDelta = %v, want %v", delta, in)
	}
}

func TestExecute_NestedJSONInputByteEqualOutput(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"items": []any{
			map[string]any{"key": "a", "payload": map[string]any{"v": float64(1)}},
			map[string]any{"key": "b", "payload": map[string]any{"v": float64(2)}},
		},
		"meta": map[string]any{"source": "upstream"},
	}
	outcome := mustExecute(t, in)
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success outcome, got %T", outcome.GetOutcome())
	}
	delta := success.GetAttributesDelta().AsMap()
	gotJSON, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("marshal delta: %v", err)
	}
	wantJSON, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal in: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("AttributesDelta JSON = %s, want %s", gotJSON, wantJSON)
	}
}

func TestExecute_ScalarValueKindsPassThrough(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"flag":  true,
		"off":   false,
		"name":  "hello",
		"count": float64(42),
		"blank": nil,
	}
	outcome := mustExecute(t, in)
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success outcome, got %T", outcome.GetOutcome())
	}
	if !success.GetChanged() {
		t.Errorf("Success.Changed = false for non-empty input, want true")
	}
	delta := success.GetAttributesDelta().AsMap()
	if !reflect.DeepEqual(delta, in) {
		t.Errorf("AttributesDelta = %#v, want %#v", delta, in)
	}
}

func TestExecute_NilAttributesIsEmpty(t *testing.T) {
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
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success outcome, got %T", outcome.GetOutcome())
	}
	delta := outcome.GetSuccess().GetAttributesDelta().AsMap()
	if len(delta) != 0 {
		t.Errorf("AttributesDelta = %v, want empty for nil request", delta)
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
