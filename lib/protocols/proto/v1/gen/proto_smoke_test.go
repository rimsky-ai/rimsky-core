// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.

package genv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestProtoSmoke_OutcomeSuccess round-trips a Success outcome variant
// with attributes_delta + tags.
func TestProtoSmoke_OutcomeSuccess(t *testing.T) {
	delta, _ := structpb.NewStruct(map[string]any{"count": float64(1)})
	src := &Outcome{Outcome: &Outcome_Success{Success: &Success{
		Changed:         true,
		ChangeSummary:   "ok",
		AttributesDelta: delta,
		Tags:            []string{"loop", "done"},
	}}}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Outcome
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !got.GetSuccess().GetChanged() {
		t.Fatalf("changed flag lost on round-trip")
	}
	if len(got.GetSuccess().GetTags()) != 2 || got.GetSuccess().GetTags()[0] != "loop" {
		t.Fatalf("tags mismatch: got %v", got.GetSuccess().GetTags())
	}
}

// TestProtoSmoke_OutcomePark round-trips a Park outcome with scratch
// (the canonical executor-managed state-carry channel per
// concept:parked-state) and tags.
func TestProtoSmoke_OutcomePark(t *testing.T) {
	src := &Outcome{Outcome: &Outcome_Park{Park: &Park{
		ResumeAt: timestamppb.New(timestamppb.Now().AsTime()),
		Scratch:  []byte("sess-abc"),
		Tags:     []string{"awaiting_remote"},
	}}}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Outcome
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.GetPark().GetResumeAt() == nil {
		t.Fatalf("resume_at should round-trip non-nil")
	}
	if len(got.GetPark().GetTags()) != 1 || got.GetPark().GetTags()[0] != "awaiting_remote" {
		t.Fatalf("park tags mismatch: %v", got.GetPark().GetTags())
	}
	if string(got.GetPark().GetScratch()) != "sess-abc" {
		t.Fatalf("scratch round-trip mismatch: got %q want %q", string(got.GetPark().GetScratch()), "sess-abc")
	}
}

// TestProtoSmoke_OutcomeAwaitAsync round-trips an AwaitAsyncCallback.
func TestProtoSmoke_OutcomeAwaitAsync(t *testing.T) {
	src := &Outcome{Outcome: &Outcome_AwaitAsync{AwaitAsync: &AwaitAsyncCallback{
		AsyncAckId:           "ack-123",
		ExpectedCompletionMs: 5000,
	}}}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Outcome
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.GetAwaitAsync().GetAsyncAckId() != "ack-123" {
		t.Fatalf("async_ack_id lost on round-trip")
	}
}

// TestProtoSmoke_AsyncCallbackBody round-trips an async callback body
// containing a Success outcome. Per TD-collapse-named-event-to-tags the
// pre-2026-06-16 events array has retired.
func TestProtoSmoke_AsyncCallbackBody(t *testing.T) {
	src := &AsyncCallbackBody{
		Outcome: &AsyncCallbackBody_Success{Success: &Success{Changed: true, ChangeSummary: "ok"}},
	}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got AsyncCallbackBody
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.GetSuccess() == nil || !got.GetSuccess().GetChanged() {
		t.Fatalf("success outcome missing or not changed")
	}
}

// TestProtoSmoke_ObservabilityCapabilitiesNewFields round-trips
// expected_attributes_schema + declared_tags on
// ObservabilityCapabilities.
func TestProtoSmoke_ObservabilityCapabilitiesNewFields(t *testing.T) {
	src := &ObservabilityCapabilities{
		ExpectedAttributesSchema: []byte(`{"type":"object"}`),
		DeclaredTags:             []string{"phase_observed", "rate_limit_observed"},
	}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ObservabilityCapabilities
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(got.GetExpectedAttributesSchema()) != string(src.ExpectedAttributesSchema) {
		t.Fatalf("expected_attributes_schema bytes mismatch")
	}
	if len(got.GetDeclaredTags()) != 2 {
		t.Fatalf("declared_tags: got %d, want 2", len(got.GetDeclaredTags()))
	}
	if got.GetDeclaredTags()[0] != "phase_observed" {
		t.Fatalf("declared_tags[0] mismatch")
	}
}
