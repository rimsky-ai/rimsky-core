// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestMakeLeafRunEvent_RunIDIsNodeRunUUID(t *testing.T) {
	t.Parallel()
	rec := LeafRunRecord{
		RunID:              "11111111-1111-1111-1111-111111111111",
		NodeAlias:          "draft",
		ChildKey:           "partition-7",
		TemplateNodeAlias:  "draft",
		TemplateHash:       "sha256-aaa",
		ExecutorName:       "claude-agent",
		Changed:            true,
		SettlingSignalType: "terminal/success",
		TerminalKind:       "complete",
		HeldClaims: []HeldClaimRef{
			{ClaimHandleID: "claim-1", Role: "acquire", ProducerName: "topics-ring", ScopeDataHash: "scope-hash-1"},
		},
	}
	observedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	ev := MakeLeafRunEvent(rec, observedAt, "analytics")
	if ev.EventType != "COMPLETE" {
		t.Errorf("eventType = %q", ev.EventType)
	}
	if ev.Run.RunID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("runId = %q (want the node-run UUID, matching claim_terminal's OpenLineageRunRef convention)", ev.Run.RunID)
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

func TestMakeLeafRunEvent_DistinctNodeRunsOfSameInstanceGetDistinctRunIDs(t *testing.T) {
	t.Parallel()
	recA := LeafRunRecord{RunID: "22222222-2222-2222-2222-222222222222", NodeAlias: "scope", TemplateNodeAlias: "scope"}
	recB := LeafRunRecord{RunID: "33333333-3333-3333-3333-333333333333", NodeAlias: "scope", TemplateNodeAlias: "scope"}
	evA := MakeLeafRunEvent(recA, time.Now(), "ns")
	evB := MakeLeafRunEvent(recB, time.Now(), "ns")
	if evA.Run.RunID == evB.Run.RunID {
		t.Fatalf("two distinct node-runs of the same instance collided on runId %q", evA.Run.RunID)
	}
	if evA.Run.RunID != recA.RunID || evB.Run.RunID != recB.RunID {
		t.Errorf("runId = %q/%q, want %q/%q", evA.Run.RunID, evB.Run.RunID, recA.RunID, recB.RunID)
	}
}

func TestMakeLeafRunEvent_UnaliasedNodeGetsGenericFallbackJobName(t *testing.T) {
	t.Parallel()
	rec := LeafRunRecord{
		RunID:  "44444444-4444-4444-4444-444444444444",
		NodeID: "node-123",
	}
	ev := MakeLeafRunEvent(rec, time.Now(), "ns")
	if ev.Job.Name != "unaliased-node-node-123" {
		t.Errorf("job.name = %q, want a generic node-id-derived fallback that does not assert a message-receiver semantic the record does not carry", ev.Job.Name)
	}
}

func TestMakeClaimTerminalEvent_OutputDatasetFromProducerAndScopeHash(t *testing.T) {
	t.Parallel()
	rec := ClaimTerminalRecord{
		ClaimHandleID:     "claim-1",
		VersionID:         "v-77",
		ProducerName:      "atomic-fs",
		ScopeDataHash:     "scope-7",
		OpenLineageRunRef: "run-99",
		FrameID:           "frame-1",
		Outcome:           "committed",
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
		ClaimHandleID:     "claim-2",
		ProducerName:      "atomic-fs",
		ScopeDataHash:     "scope-abc",
		OpenLineageRunRef: "run-2",
		FrameID:           "frame-2",
		Outcome:           "abandoned",
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
	claimTerminal, ok := ev.Outputs[0].Facets["rimsky_claim_terminal"].(map[string]any)
	if !ok {
		t.Fatalf("rimsky_claim_terminal facet missing or wrong type: %+v", ev.Outputs[0].Facets)
	}
	if cause, _ := claimTerminal["cause"].(string); cause != "sibling_cancel" {
		t.Errorf("rimsky_claim_terminal.cause = %q want sibling_cancel", cause)
	}
	if claimTerminal["_producer"] != openLineageProducerURI {
		t.Errorf("rimsky_claim_terminal._producer = %v, want %q", claimTerminal["_producer"], openLineageProducerURI)
	}
	if _, ok := claimTerminal["_schemaURL"].(string); !ok {
		t.Errorf("rimsky_claim_terminal._schemaURL missing")
	}
}

func TestMakeLeafRunEvent_CustomFacetsLiveUnderRunFacetsWithProducerEnvelope(t *testing.T) {
	t.Parallel()
	rec := LeafRunRecord{
		RunID:     "44444444-4444-4444-4444-444444444444",
		NodeAlias: "draft",
		HeldClaims: []HeldClaimRef{
			{ClaimHandleID: "claim-1", Role: "acquire", ProducerName: "topics-ring", ScopeDataHash: "scope-hash-1"},
		},
	}
	ev := MakeLeafRunEvent(rec, time.Now(), "ns")

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := decoded["facets"]; present {
		t.Errorf("top-level facets key present in wire JSON: %s (OpenLineage RunEvent has no such member)", raw)
	}

	runFacets, ok := ev.Run.Facets["rimsky"].(map[string]any)
	if !ok {
		t.Fatalf("run.facets.rimsky missing or wrong type: %+v", ev.Run.Facets)
	}
	if runFacets["_producer"] != openLineageProducerURI {
		t.Errorf("run.facets.rimsky._producer = %v, want %q", runFacets["_producer"], openLineageProducerURI)
	}
	if schemaURL, _ := runFacets["_schemaURL"].(string); schemaURL == "" {
		t.Errorf("run.facets.rimsky._schemaURL missing")
	}

	if len(ev.Inputs) != 1 {
		t.Fatalf("inputs: %+v", ev.Inputs)
	}
	heldClaim, ok := ev.Inputs[0].Facets["rimsky_held_claim"].(map[string]any)
	if !ok {
		t.Fatalf("inputs[0].facets.rimsky_held_claim missing or wrong type: %+v", ev.Inputs[0].Facets)
	}
	if heldClaim["claim_handle_id"] != "claim-1" {
		t.Errorf("rimsky_held_claim.claim_handle_id = %v, want claim-1", heldClaim["claim_handle_id"])
	}
	if heldClaim["_producer"] != openLineageProducerURI {
		t.Errorf("rimsky_held_claim._producer = %v, want %q", heldClaim["_producer"], openLineageProducerURI)
	}
	if schemaURL, _ := heldClaim["_schemaURL"].(string); schemaURL == "" {
		t.Errorf("rimsky_held_claim._schemaURL missing")
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

	e := NewEmitter(srv.URL, "")
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
	e := NewEmitter(srv.URL, "")
	if err := e.Send(context.Background(), Event{EventType: "COMPLETE"}); err == nil {
		t.Errorf("expected error on 500")
	}
}

func TestEmitter_EmptyBackendErrors(t *testing.T) {
	t.Parallel()
	e := NewEmitter("", "")
	if err := e.Send(context.Background(), Event{EventType: "COMPLETE"}); err == nil {
		t.Error("expected Send to error on an empty backend URL, not silently no-op " +
			"(a silent no-op lets tick() advance the cursor and permanently discard the record)")
	}
}

func TestEmitter_AddsBearerTokenWhenConfigured(t *testing.T) {
	t.Parallel()
	var (
		mu   sync.Mutex
		auth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	e := NewEmitter(srv.URL, "secret-token")
	if err := e.Send(context.Background(), Event{EventType: "COMPLETE"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer secret-token")
	}
}

func TestEmitter_NoBearerTokenNoAuthHeader(t *testing.T) {
	t.Parallel()
	var (
		mu     sync.Mutex
		hasKey bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		_, hasKey = r.Header["Authorization"]
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	e := NewEmitter(srv.URL, "")
	if err := e.Send(context.Background(), Event{EventType: "COMPLETE"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hasKey {
		t.Errorf("Authorization header present when no token configured")
	}
}
