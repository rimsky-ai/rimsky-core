// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.

package genv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestProtoSmoke_NamedEvent round-trips a NamedEvent message and asserts
// the new fields land where expected.
func TestProtoSmoke_NamedEvent(t *testing.T) {
	src := &NamedEvent{
		Name:    "rate_limit_observed",
		Payload: []byte(`{"reset_at": "2026-05-08T13:00:00Z"}`),
	}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got NamedEvent
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.GetName() != src.Name {
		t.Fatalf("name: got %q, want %q", got.GetName(), src.Name)
	}
	if string(got.GetPayload()) != string(src.Payload) {
		t.Fatalf("payload bytes mismatch")
	}
}

// TestProtoSmoke_ParkRequested round-trips a ParkRequested terminal
// event with all fields populated.
func TestProtoSmoke_ParkRequested(t *testing.T) {
	src := &ParkRequested{
		Reason:       "rate_limit",
		Payload:      []byte(`{"agent_state": "..."}`),
		ResumeAt:     timestamppb.New(timestamppb.Now().AsTime()),
		SessionToken: "sess-abc-123",
	}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ParkRequested
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.GetReason() != src.Reason {
		t.Fatalf("reason: got %q, want %q", got.GetReason(), src.Reason)
	}
	if string(got.GetPayload()) != string(src.Payload) {
		t.Fatalf("payload bytes mismatch")
	}
	if got.GetSessionToken() != src.SessionToken {
		t.Fatalf("session_token: got %q, want %q", got.GetSessionToken(), src.SessionToken)
	}
	if got.GetResumeAt() == nil {
		t.Fatalf("resume_at should round-trip non-nil")
	}
}

// TestProtoSmoke_ResumeContext round-trips a ResumeContext.
func TestProtoSmoke_ResumeContext(t *testing.T) {
	src := &ResumeContext{
		Payload:      []byte(`{"agent_state": "..."}`),
		SessionToken: "sess-abc-123",
		ResumeReason: "deadline_elapsed",
	}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ResumeContext
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(got.GetPayload()) != string(src.Payload) {
		t.Fatalf("payload bytes mismatch")
	}
	if got.GetSessionToken() != src.SessionToken {
		t.Fatalf("session_token mismatch")
	}
	if got.GetResumeReason() != src.ResumeReason {
		t.Fatalf("resume_reason mismatch")
	}
}

// TestProtoSmoke_AsyncCallbackBody round-trips an async callback body
// containing both events and a terminal Complete verdict.
func TestProtoSmoke_AsyncCallbackBody(t *testing.T) {
	src := &AsyncCallbackBody{
		Events: []*NamedEvent{
			{Name: "phase_observed", Payload: []byte(`{"phase":"warmup"}`)},
			{Name: "phase_observed", Payload: []byte(`{"phase":"steady"}`)},
		},
		Complete: &Complete{Changed: true, ChangeSummary: "ok"},
	}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got AsyncCallbackBody
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.GetEvents()) != 2 {
		t.Fatalf("events: got %d, want 2", len(got.GetEvents()))
	}
	if got.GetComplete() == nil || !got.GetComplete().GetChanged() {
		t.Fatalf("complete missing or not changed")
	}
}

// TestProtoSmoke_ObservabilityCapabilitiesNewFields round-trips the new
// userdata_schema and declared_events fields on ObservabilityCapabilities.
func TestProtoSmoke_ObservabilityCapabilitiesNewFields(t *testing.T) {
	src := &ObservabilityCapabilities{
		UserdataSchema: []byte(`{"type":"object"}`),
		DeclaredEvents: []string{"phase_observed", "rate_limit_observed"},
	}
	bytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ObservabilityCapabilities
	if err := proto.Unmarshal(bytes, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(got.GetUserdataSchema()) != string(src.UserdataSchema) {
		t.Fatalf("userdata_schema bytes mismatch")
	}
	if len(got.GetDeclaredEvents()) != 2 {
		t.Fatalf("declared_events: got %d, want 2", len(got.GetDeclaredEvents()))
	}
	if got.GetDeclaredEvents()[0] != "phase_observed" {
		t.Fatalf("declared_events[0] mismatch")
	}
}

// TestProtoSmoke_ExecuteEventOneofWithNewVariants verifies the new oneof
// variants (named_event, park_requested) marshal and dispatch correctly.
func TestProtoSmoke_ExecuteEventOneofWithNewVariants(t *testing.T) {
	cases := []struct {
		name string
		evt  *ExecuteEvent
	}{
		{"named_event", &ExecuteEvent{Event: &ExecuteEvent_NamedEvent{NamedEvent: &NamedEvent{Name: "x"}}}},
		{"park_requested", &ExecuteEvent{Event: &ExecuteEvent_ParkRequested{ParkRequested: &ParkRequested{Reason: "rate_limit"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bytes, err := proto.Marshal(tc.evt)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got ExecuteEvent
			if err := proto.Unmarshal(bytes, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.GetEvent() == nil {
				t.Fatalf("oneof variant lost on round-trip")
			}
		})
	}
}
