// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func TestParseCallbackBody_NewShape_Complete(t *testing.T) {
	body := map[string]any{
		"complete": map[string]any{
			"attributes_delta": map[string]any{"k": "v"},
			"changed":          true,
			"change_summary":   "applied",
		},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	c, ok := ev.Event.(*genv1.ExecuteEvent_Complete)
	if !ok {
		t.Fatalf("expected Complete, got %T", ev.Event)
	}
	if !c.Complete.Changed {
		t.Errorf("changed not propagated")
	}
	if c.Complete.ChangeSummary != "applied" {
		t.Errorf("change_summary=%q want=applied", c.Complete.ChangeSummary)
	}
	if got := c.Complete.GetAttributesDelta().AsMap(); got["k"] != "v" {
		t.Errorf("attributes_delta=%v want k=v", got)
	}
}

func TestParseCallbackBody_NewShape_Blocked(t *testing.T) {
	body := map[string]any{
		"blocked": map[string]any{
			"reason":  "missing-input",
			"context": map[string]any{"hint": "wait"},
		},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	b, ok := ev.Event.(*genv1.ExecuteEvent_Blocked)
	if !ok {
		t.Fatalf("expected Blocked, got %T", ev.Event)
	}
	if b.Blocked.Reason != "missing-input" {
		t.Errorf("reason=%q want missing-input", b.Blocked.Reason)
	}
	if got := b.Blocked.GetContext().AsMap(); got["hint"] != "wait" {
		t.Errorf("context=%v", got)
	}
}

func TestParseCallbackBody_NewShape_Errored(t *testing.T) {
	body := map[string]any{
		"errored": map[string]any{
			"error_class": "boom",
			"payload":     map[string]any{"detail": "x"},
		},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	e, ok := ev.Event.(*genv1.ExecuteEvent_Errored)
	if !ok {
		t.Fatalf("expected Errored, got %T", ev.Event)
	}
	if e.Errored.ErrorClass != "boom" {
		t.Errorf("error_class=%q want boom", e.Errored.ErrorClass)
	}
}

func TestParseCallbackBody_NewShape_ParkRequested_Base64Payload(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("opaque-bytes"))
	resumeAt := time.Date(2026, 5, 9, 15, 30, 0, 0, time.UTC).Format(time.RFC3339)
	body := map[string]any{
		"park_requested": map[string]any{
			"reason":        "rate_limit",
			"payload":       encoded,
			"session_token": "sess-1",
			"resume_at":     resumeAt,
		},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	p, ok := ev.Event.(*genv1.ExecuteEvent_ParkRequested)
	if !ok {
		t.Fatalf("expected ParkRequested, got %T", ev.Event)
	}
	if string(p.ParkRequested.Payload) != "opaque-bytes" {
		t.Errorf("payload=%q want opaque-bytes", string(p.ParkRequested.Payload))
	}
	if p.ParkRequested.SessionToken != "sess-1" {
		t.Errorf("session_token=%q", p.ParkRequested.SessionToken)
	}
	if p.ParkRequested.ResumeAt == nil {
		t.Fatalf("resume_at not propagated")
	}
	if got := p.ParkRequested.ResumeAt.AsTime(); !got.Equal(time.Date(2026, 5, 9, 15, 30, 0, 0, time.UTC)) {
		t.Errorf("resume_at=%v", got)
	}
}

func TestParseCallbackBody_NewShape_ParkRequested_LiteralPayload(t *testing.T) {
	// Non-base64 payload is tolerated as a literal string.
	body := map[string]any{
		"park_requested": map[string]any{
			"reason":        "rate_limit",
			"payload":       "!!! not base64 !!!",
			"session_token": "sess-2",
		},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	p, ok := ev.Event.(*genv1.ExecuteEvent_ParkRequested)
	if !ok {
		t.Fatalf("expected ParkRequested, got %T", ev.Event)
	}
	if string(p.ParkRequested.Payload) != "!!! not base64 !!!" {
		t.Errorf("payload=%q", string(p.ParkRequested.Payload))
	}
	if p.ParkRequested.ResumeAt != nil {
		t.Errorf("resume_at should be nil when absent, got %v", p.ParkRequested.ResumeAt)
	}
}

func TestParseCallbackBody_LegacyShape_Complete(t *testing.T) {
	body := map[string]any{
		"type":             "complete",
		"attributes_delta": map[string]any{"x": float64(1)},
		"changed":          false,
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	c, ok := ev.Event.(*genv1.ExecuteEvent_Complete)
	if !ok {
		t.Fatalf("expected Complete, got %T", ev.Event)
	}
	if c.Complete.Changed {
		t.Errorf("changed=true want false")
	}
	if got := c.Complete.GetAttributesDelta().AsMap(); got["x"] != float64(1) {
		t.Errorf("attributes_delta=%v", got)
	}
}

func TestParseCallbackBody_LegacyShape_Errored(t *testing.T) {
	body := map[string]any{
		"type":        "errored",
		"error_class": "ec",
		"payload":     map[string]any{"why": "x"},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	if _, ok := ev.Event.(*genv1.ExecuteEvent_Errored); !ok {
		t.Fatalf("expected Errored, got %T", ev.Event)
	}
}

func TestParseCallbackBody_RejectsMultipleTerminals(t *testing.T) {
	// Two terminal fields → reject. Mirrors the supervisor's parser.
	body := map[string]any{
		"complete": map[string]any{},
		"errored":  map[string]any{"error_class": "x"},
	}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for multi-terminal body, got nil")
	}
}

func TestParseCallbackBody_NoTerminal(t *testing.T) {
	if _, err := parseCallbackBody(map[string]any{"events": []any{}}); err == nil {
		t.Fatal("expected error for body with no terminal field")
	}
}

func TestReceiver_RegisterThenHandle(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-rh"
	ch := r.Register(ackID)

	postCallback(t, r.URL(), ackID, map[string]any{
		"complete": map[string]any{"changed": true},
	})

	select {
	case ev := <-ch:
		if ev == nil {
			t.Fatal("nil ev")
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_Complete); !ok {
			t.Fatalf("expected Complete, got %T", ev.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}

func TestReceiver_HandleThenRegister(t *testing.T) {
	// Late-registration path: the POST arrives first; handle() buffers
	// the synthesized event on the channel; Register() then returns the
	// already-buffered channel.
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-hr"
	postCallback(t, r.URL(), ackID, map[string]any{
		"complete": map[string]any{"changed": false},
	})

	// Tiny pause to ensure the POST handler has finished buffering before
	// Register pulls the channel out of the map.
	time.Sleep(100 * time.Millisecond)

	ch := r.Register(ackID)
	select {
	case ev := <-ch:
		if _, ok := ev.Event.(*genv1.ExecuteEvent_Complete); !ok {
			t.Fatalf("expected Complete, got %T", ev.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for buffered callback")
	}
}

func TestReceiver_DuplicateCallback_Discarded(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-dup"
	ch := r.Register(ackID)

	postCallback(t, r.URL(), ackID, map[string]any{
		"complete": map[string]any{"changed": true},
	})
	postCallback(t, r.URL(), ackID, map[string]any{
		"errored": map[string]any{"error_class": "later"},
	})

	first := <-ch
	if _, ok := first.Event.(*genv1.ExecuteEvent_Complete); !ok {
		t.Fatalf("expected first=Complete, got %T", first.Event)
	}
	// Channel buffer is 1; the second POST must be silently discarded.
	select {
	case extra, ok := <-ch:
		if ok && extra != nil {
			t.Fatalf("unexpected duplicate callback delivered: %T", extra.Event)
		}
	case <-time.After(150 * time.Millisecond):
		// expected — no second event delivered
	}
}

func TestReceiver_ConcurrentRegisterAndHandle(t *testing.T) {
	// Race detector should be clean: many goroutines Register & POST in
	// parallel, every ackID must yield exactly one delivered event.
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			ackID := fmt.Sprintf("ack-%d", i)
			// Half register first, half post first — exercises both
			// branches of the register/handle cooperation.
			if i%2 == 0 {
				ch := r.Register(ackID)
				postCallback(t, r.URL(), ackID, map[string]any{
					"complete": map[string]any{"changed": false},
				})
				select {
				case <-ch:
				case <-time.After(2 * time.Second):
					t.Errorf("ack=%s: timed out", ackID)
				}
			} else {
				postCallback(t, r.URL(), ackID, map[string]any{
					"complete": map[string]any{"changed": true},
				})
				ch := r.Register(ackID)
				select {
				case <-ch:
				case <-time.After(2 * time.Second):
					t.Errorf("ack=%s: timed out", ackID)
				}
			}
		}()
	}
	wg.Wait()
}

func TestReceiver_AdvertiseHostFallback(t *testing.T) {
	// When AdvertiseHost is "0.0.0.0" the receiver must NOT advertise that
	// to executors (it isn't a routable peer address). Falls back to
	// 127.0.0.1.
	r, err := StartCallbackReceiver(ReceiverOptions{
		BindHost:      "127.0.0.1",
		AdvertiseHost: "0.0.0.0",
	})
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()
	if want := "127.0.0.1"; !contains(r.URL(), want) {
		t.Errorf("URL=%s want host=%s", r.URL(), want)
	}
}

func TestReceiver_HandleRejectsMalformedJSON(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	resp, err := http.Post(r.URL()+"/v1/callback/x", "application/json", bytes.NewReader([]byte("not-json")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}

func TestReceiver_HandleRejectsMultiTerminal(t *testing.T) {
	// The HTTP layer should propagate parseCallbackBody's rejection of
	// multi-terminal bodies as 400 — surfacing executor defects to the
	// conformance suite the same way the supervisor would in production.
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	body, _ := json.Marshal(map[string]any{
		"complete": map[string]any{},
		"blocked":  map[string]any{"reason": "x"},
	})
	resp, err := http.Post(r.URL()+"/v1/callback/x", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for multi-terminal body", resp.StatusCode)
	}
}

func postCallback(t *testing.T, baseURL, ackID string, body map[string]any) {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(baseURL+"/v1/callback/"+ackID, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("postCallback status=%d", resp.StatusCode)
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
