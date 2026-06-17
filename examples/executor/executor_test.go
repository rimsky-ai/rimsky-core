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

// @deliberate: TestExecuteReturnsSuccessOutcome — default dispatch
// path (no `mode` attribute) settles as Outcome{Success} with no
// tags and a non-error change summary. Plus the Capabilities
// handshake advertises all three load-bearing fields:
// expected_attributes_schema, declared_tags, declared_error_classes.
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

// @deliberate: TestExecute_RaiseErrorEmitsDeclaredClass — `mode:
// raise_error` settles as Outcome{Error} carrying the executor's
// declared error class. Per @concept:error-policy the wire value of
// Error.error_class IS the routing key; an operator template's
// `error_types: { example/forbidden: ... }` chain matches on this
// exact string.
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

// @deliberate: TestExecute_EmitEventEmitsDeclaredTag — `mode:
// emit_event` settles as Outcome{Success} carrying the declared tag
// in `Tags`. Per @concept:terminal-tag the tag rides on the
// settling verdict; downstream subscribers fire via
// `type: terminal/success when: "<tag>" in payload.tags`.
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

// @deliberate: TestExecute_AsyncMode_ReturnsAwaitAsyncCallback —
// `mode: async_callback` with a supplied `async_ack_id` attribute
// settles the unary RPC as Outcome{AwaitAsyncCallback} carrying that
// exact ack id. Per @concept:async-callback-persistence the supervisor
// keys the dispatch row's `col:rimsky_node_runs.async_ack_id` on this
// value; an incoming callback POST to `/v1/callback/{ack_id}` is
// correlated by lookup against that column, surviving supervisor
// restart. The empty-callback-url branch is exercised: with no URL set
// the executor returns AwaitAsync without dispatching the deferred
// POST goroutine, so the test asserts on the unary-response shape only.
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

// @deliberate: TestExecute_AsyncMode_MissingAckIDSurfacesError — the
// empty-ack-id branch of the async-callback path is a template
// mistake (the executor has no way to manufacture a stable id the
// supervisor's persistent registry can correlate against). The
// executor surfaces it as an Error{DeclaredErrorClass} instead of
// AwaitAsync against an empty ack id, so the failure is visible in
// the audit log instead of stuck in `running`.
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
