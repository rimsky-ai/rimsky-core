// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestMakeLeafRunEvent_ShapesRunIDFromInstanceAndChildKey(t *testing.T) {
	t.Parallel()
	rec := LeafRunRecord{
		RunID:             "run-1",
		NodeAlias:         "draft",
		ChildKey:          "partition-7",
		TemplateNodeAlias: "draft",
		TemplateHash:      "sha256-aaa",
		ExecutorName:      "claude-agent",
		Changed:           true,
		LastOutcome:       "fresh_changed",
		TerminalKind:      "complete",
		HeldClaims: []HeldClaimRef{
			{ClaimHandleID: "claim-1", Role: "acquire", ProducerName: "topics-ring", ScopeDataHash: "scope-hash-1"},
		},
	}
	observedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	ev := MakeLeafRunEvent(rec, observedAt, "inst-1", "analytics")
	if ev.EventType != "COMPLETE" {
		t.Errorf("eventType = %q", ev.EventType)
	}
	if ev.Run.RunID != "inst-1/partition-7" {
		t.Errorf("runId = %q (want inst-1/partition-7)", ev.Run.RunID)
	}
	if ev.Job.Namespace != "analytics" {
		t.Errorf("job.namespace = %q", ev.Job.Namespace)
	}
	if ev.Job.Name != "draft" {
		t.Errorf("job.name = %q", ev.Job.Name)
	}
	if len(ev.Inputs) != 1 || ev.Inputs[0].Namespace != "topics-ring" || ev.Inputs[0].Name != "scope-hash-1" {
		t.Errorf("inputs = %+v", ev.Inputs)
	}
	if ev.EventTime != observedAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("eventTime = %q", ev.EventTime)
	}
}

func TestMakeLeafRunEvent_NoChildKey_RunIDIsInstance(t *testing.T) {
	t.Parallel()
	rec := LeafRunRecord{RunID: "run-2", NodeAlias: "scope", TemplateNodeAlias: "scope"}
	ev := MakeLeafRunEvent(rec, time.Now(), "inst-2", "ns")
	if ev.Run.RunID != "inst-2" {
		t.Errorf("runId = %q (want inst-2)", ev.Run.RunID)
	}
}

func TestMakeClaimTerminalEvent_OutputDatasetFromProducerAndScopeHash(t *testing.T) {
	t.Parallel()
	rec := ClaimTerminalRecord{
		ClaimHandleID: "claim-1",
		VersionID:     "v-77",
		ProducerName:  "atomic-fs",
		ScopeDataHash: "scope-7",
		ParentRunID:   "run-99",
		FrameID:       "frame-1",
		Outcome:       "committed",
	}
	observedAt := time.Date(2026, 5, 15, 13, 0, 0, 0, time.UTC)
	ev := MakeClaimTerminalEvent(rec, observedAt, "ns")
	if ev.EventType != "COMPLETE" {
		t.Errorf("eventType = %q", ev.EventType)
	}
	if len(ev.Outputs) != 1 || ev.Outputs[0].Namespace != "atomic-fs" || ev.Outputs[0].Name != "scope-7" {
		t.Errorf("outputs = %+v", ev.Outputs)
	}
	if ev.Run.RunID != "run-99" {
		t.Errorf("runId = %q (want run-99)", ev.Run.RunID)
	}
	if ev.Job.Name != "atomic-fs.commit" {
		t.Errorf("job.name = %q", ev.Job.Name)
	}
}

func TestMakeClaimTerminalEvent_AbandonedFiresAbortEvent(t *testing.T) {
	t.Parallel()
	rec := ClaimTerminalRecord{
		ClaimHandleID: "claim-2",
		ProducerName:  "atomic-fs",
		ScopeDataHash: "scope-abc",
		ParentRunID:   "run-2",
		FrameID:       "frame-2",
		Outcome:       "abandoned",
	}
	ev := MakeClaimTerminalEvent(rec, time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC), "ns")
	if ev.EventType != "ABORT" {
		t.Errorf("eventType = %q want ABORT", ev.EventType)
	}
	if ev.Job.Name != "atomic-fs.abandon" {
		t.Errorf("job.name = %q want atomic-fs.abandon", ev.Job.Name)
	}
}

func TestMakeClaimTerminalEvent_ForceCancelledCarriesCause(t *testing.T) {
	t.Parallel()
	rec := ClaimTerminalRecord{
		ClaimHandleID: "claim-3",
		ProducerName:  "atomic-fs",
		ScopeDataHash: "scope-xyz",
		FrameID:       "frame-3",
		Outcome:       "force_cancelled",
		Cause:         "sibling_cancel",
	}
	ev := MakeClaimTerminalEvent(rec, time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC), "ns")
	if ev.EventType != "ABORT" {
		t.Errorf("eventType = %q want ABORT", ev.EventType)
	}
	if cause, _ := ev.Outputs[0].Facets["rimsky_cause"].(string); cause != "sibling_cancel" {
		t.Errorf("rimsky_cause facet = %q want sibling_cancel", cause)
	}
}

func TestEmitter_PostsJSONToBackend(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		received []Event
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/lineage" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing Content-Type")
		}
		body, _ := io.ReadAll(r.Body)
		var ev Event
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Errorf("decode: %v", err)
		}
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := NewEmitter(srv.URL)
	ev := Event{EventType: "COMPLETE", EventTime: time.Now().Format(time.RFC3339), Run: RunRef{RunID: "r"}, Job: JobRef{Namespace: "n", Name: "j"}}
	if err := e.Send(context.Background(), ev); err != nil {
		t.Fatalf("send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0].EventType != "COMPLETE" {
		t.Errorf("backend received %+v", received)
	}
}

func TestEmitter_ReportsNon2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	e := NewEmitter(srv.URL)
	if err := e.Send(context.Background(), Event{EventType: "COMPLETE"}); err == nil {
		t.Errorf("expected error on 500")
	}
}

func TestEmitter_EmptyBackendIsNoOp(t *testing.T) {
	t.Parallel()
	e := NewEmitter("")
	if err := e.Send(context.Background(), Event{EventType: "COMPLETE"}); err != nil {
		t.Errorf("empty backend should be no-op: %v", err)
	}
}
