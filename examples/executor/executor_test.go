// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// @story: executor-protocol — fast in-process tests pinning the
// dispatch happy path + declared-class + tagged-Success modes against
// the unary executor protocol.

package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestExecuteReturnsSuccessOutcome(t *testing.T) {
	e := &Executor{}
	outcome, err := e.Execute(context.Background(), &genv1.ExecuteRequest{})
	if err != nil {
		return
	}
	success, ok := outcome.GetOutcome().(*genv1.Outcome_Success)
	if !ok {
		t.Fatalf("expected Outcome_Success, got %T", outcome.GetOutcome())
	}
	if len(success.Success.GetTags()) != 0 {
		t.Errorf("default Success.Tags should be empty, got %v", success.Success.GetTags())
	}

	caps, err := e.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.GetExpectedAttributesSchema()) == 0 {
		t.Errorf("expected_attributes_schema is empty")
	}
	if len(caps.GetDeclaredTags()) == 0 {
		t.Errorf("declared_tags is empty")
	}
	if len(caps.GetDeclaredErrorClasses()) == 0 {
		t.Errorf("declared_error_classes is empty")
	}
}

func TestExecute_RaiseErrorEmitsDeclaredClass(t *testing.T) {
	e := &Executor{}
	req := mustExecuteRequest(t, map[string]any{"mode": "raise_error"})
	outcome, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	errOut, ok := outcome.GetOutcome().(*genv1.Outcome_Error)
	if !ok {
		t.Fatalf("expected Outcome_Error, got %T", outcome.GetOutcome())
	}
	if errOut.Error.GetErrorClass() != DeclaredErrorClass {
		t.Errorf("Error.error_class = %q, want %q",
			errOut.Error.GetErrorClass(), DeclaredErrorClass)
	}
}

func TestExecute_EmitEventEmitsDeclaredTag(t *testing.T) {
	e := &Executor{}
	req := mustExecuteRequest(t, map[string]any{"mode": "emit_event"})
	outcome, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	success, ok := outcome.GetOutcome().(*genv1.Outcome_Success)
	if !ok {
		t.Fatalf("expected Outcome_Success, got %T", outcome.GetOutcome())
	}
	tags := success.Success.GetTags()
	if len(tags) != 1 || tags[0] != DeclaredTagName {
		t.Errorf("Success.Tags = %v, want [%q]", tags, DeclaredTagName)
	}
}

func TestExecute_AsyncMode_ReturnsAwaitAsyncCallback(t *testing.T) {
	e := &Executor{}
	const wantAck = "ack-async-unit-test-1"
	req := mustExecuteRequest(t, map[string]any{
		"mode":         "async_callback",
		"async_ack_id": wantAck,
	})
	outcome, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	await, ok := outcome.GetOutcome().(*genv1.Outcome_AwaitAsync)
	if !ok {
		t.Fatalf("expected Outcome_AwaitAsync, got %T", outcome.GetOutcome())
	}
	if got := await.AwaitAsync.GetAsyncAckId(); got != wantAck {
		t.Errorf("AwaitAsyncCallback.async_ack_id = %q, want %q", got, wantAck)
	}
}

func TestExecute_AsyncMode_MissingAckIDSurfacesError(t *testing.T) {
	e := &Executor{}
	req := mustExecuteRequest(t, map[string]any{"mode": "async_callback"})
	outcome, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	errOut, ok := outcome.GetOutcome().(*genv1.Outcome_Error)
	if !ok {
		t.Fatalf("expected Outcome_Error (empty async_ack_id), got %T", outcome.GetOutcome())
	}
	if errOut.Error.GetErrorClass() != DeclaredErrorClass {
		t.Errorf("Error.error_class = %q, want %q",
			errOut.Error.GetErrorClass(), DeclaredErrorClass)
	}
}

func mustExecuteRequest(t *testing.T, attrs map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	attrStruct, err := structpb.NewStruct(attrs)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return &genv1.ExecuteRequest{
		NodeId:     "00000000-0000-0000-0000-000000000001",
		InstanceId: "00000000-0000-0000-0000-000000000002",
		Attributes: attrStruct,
	}
}
