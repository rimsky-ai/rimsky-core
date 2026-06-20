// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package emit_message

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

func TestExecute_NilEmitCallbackReturnsNamedError(t *testing.T) {
	t.Parallel()
	h := New()
	req := &genv1.ExecuteRequest{
		DispatchId: "00000000-0000-0000-0000-000000000001",
		NodeId:     "00000000-0000-0000-0000-000000000002",
	}
	outcome, err := h.Execute(context.Background(), req, executor.HandlerContext{})
	if err == nil {
		t.Fatalf("expected error for nil EmitCascadeMessage, got nil")
	}
	if outcome != nil {
		t.Errorf("expected nil outcome on error, got %v", outcome)
	}
	msg := err.Error()
	if !strings.Contains(msg, "EmitCascadeMessage is nil") {
		t.Errorf("error message %q does not name the nil EmitCascadeMessage callback", msg)
	}
	if !strings.Contains(msg, "emits_message") {
		t.Errorf("error message %q does not name the emits_message contract", msg)
	}
}

func TestExecute_CallbackReceivesMarshaledAttributes(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"key":  "value",
		"n":    float64(7),
		"flag": true,
		"nested": map[string]any{
			"inner": "deep",
		},
	}
	attrStruct, err := structpb.NewStruct(in)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	req := &genv1.ExecuteRequest{
		DispatchId: "00000000-0000-0000-0000-000000000001",
		NodeId:     "00000000-0000-0000-0000-000000000002",
		Attributes: attrStruct,
	}

	var capturedBody []byte
	emit := func(ctx context.Context, body []byte) (shared.UUID, bool, error) {
		capturedBody = body
		return shared.UUID(uuid.New()), false, nil
	}
	h := New()
	outcome, err := h.Execute(context.Background(), req, executor.HandlerContext{EmitCascadeMessage: emit})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if outcome == nil {
		t.Fatalf("Execute returned nil outcome")
	}
	if capturedBody == nil {
		t.Fatalf("EmitCascadeMessage callback was not invoked")
	}
	var got map[string]any
	if err := json.Unmarshal(capturedBody, &got); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("callback received body %v, want %v", got, in)
	}
}

func TestExecute_OutcomeIsSuccessWithChangedFalse(t *testing.T) {
	t.Parallel()
	req := &genv1.ExecuteRequest{
		DispatchId: "00000000-0000-0000-0000-000000000001",
		NodeId:     "00000000-0000-0000-0000-000000000002",
	}
	emit := func(ctx context.Context, body []byte) (shared.UUID, bool, error) {
		return shared.UUID(uuid.New()), false, nil
	}
	h := New()
	outcome, err := h.Execute(context.Background(), req, executor.HandlerContext{EmitCascadeMessage: emit})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success outcome, got %T", outcome.GetOutcome())
	}
	if success.GetChanged() {
		t.Errorf("Success.Changed = true, want false")
	}
}
