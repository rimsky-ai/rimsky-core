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

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

func extractStreamCloseOutcome(t *testing.T, ev *genv1.ExecuteEvent) any {
	t.Helper()
	sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	if !ok {
		t.Fatalf("expected StreamClose, got %T", ev.Event)
	}
	return sc.StreamClose.Outcome
}

func TestParseCallbackBody_NewShape_Success(t *testing.T) {
	body := map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"k": "v"},
			"changed":          true,
			"change_summary":   "applied",
		},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	oc := extractStreamCloseOutcome(t, ev)
	c, ok := oc.(*genv1.StreamClose_Success)
	if !ok {
		t.Fatalf("expected Success, got %T", oc)
	}
	if !c.Success.Changed {
		t.Errorf("changed not propagated")
	}
	if c.Success.ChangeSummary != "applied" {
		t.Errorf("change_summary=%q want=applied", c.Success.ChangeSummary)
	}
	if got := c.Success.GetAttributesDelta().AsMap(); got["k"] != "v" {
		t.Errorf("attributes_delta=%v want k=v", got)
	}
}

func TestParseCallbackBody_NewShape_Error(t *testing.T) {
	body := map[string]any{
		"error": map[string]any{
			"error_class": "boom",
			"payload":     map[string]any{"detail": "x"},
		},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	oc := extractStreamCloseOutcome(t, ev)
	e, ok := oc.(*genv1.StreamClose_Error)
	if !ok {
		t.Fatalf("expected Error, got %T", oc)
	}
	if e.Error.ErrorClass != "boom" {
		t.Errorf("error_class=%q want boom", e.Error.ErrorClass)
	}
}

func TestParseCallbackBody_NewShape_Park_Base64Payload(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("opaque-bytes"))
	resumeAt := time.Date(2026, 5, 9, 15, 30, 0, 0, time.UTC).Format(time.RFC3339)
	body := map[string]any{
		"park": map[string]any{
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
	oc := extractStreamCloseOutcome(t, ev)
	p, ok := oc.(*genv1.StreamClose_Park)
	if !ok {
		t.Fatalf("expected Park, got %T", oc)
	}
	if string(p.Park.Payload) != "opaque-bytes" {
		t.Errorf("payload=%q want opaque-bytes", string(p.Park.Payload))
	}
	if p.Park.SessionToken != "sess-1" {
		t.Errorf("session_token=%q", p.Park.SessionToken)
	}
	if p.Park.ResumeAt == nil {
		t.Fatalf("resume_at not propagated")
	}
	if got := p.Park.ResumeAt.AsTime(); !got.Equal(time.Date(2026, 5, 9, 15, 30, 0, 0, time.UTC)) {
		t.Errorf("resume_at=%v", got)
	}
}

func TestParseCallbackBody_NewShape_Park_LiteralPayload(t *testing.T) {
	// Non-base64 payload is tolerated as a literal string.
	body := map[string]any{
		"park": map[string]any{
			"reason":        "rate_limit",
			"payload":       "!!! not base64 !!!",
			"session_token": "sess-2",
		},
	}
	ev, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	oc := extractStreamCloseOutcome(t, ev)
	p, ok := oc.(*genv1.StreamClose_Park)
	if !ok {
		t.Fatalf("expected Park, got %T", oc)
	}
	if string(p.Park.Payload) != "!!! not base64 !!!" {
		t.Errorf("payload=%q", string(p.Park.Payload))
	}
	if p.Park.ResumeAt != nil {
		t.Errorf("resume_at should be nil when absent, got %v", p.Park.ResumeAt)
	}
}

func TestParseCallbackBody_RejectsMultipleOutcomes(t *testing.T) {
	body := map[string]any{
		"success": map[string]any{},
		"error":   map[string]any{"error_class": "x"},
	}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for multi-outcome body, got nil")
	}
}

func TestParseCallbackBody_NoOutcome(t *testing.T) {
	if _, err := parseCallbackBody(map[string]any{"events": []any{}}); err == nil {
		t.Fatal("expected error for body with no outcome field")
	}
}

func TestParseCallbackBody_RejectsLegacyTypeDiscriminator(t *testing.T) {
	// The legacy {type: "complete"|"blocked"|"errored"} shape is no
	// longer accepted post-2026-05-12.
	body := map[string]any{
		"type":             "complete",
		"attributes_delta": map[string]any{},
	}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for legacy type-discriminator body, got nil")
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
		"success": map[string]any{"changed": true},
	})

	select {
	case ev := <-ch:
		if ev == nil {
			t.Fatal("nil ev")
		}
		oc := extractStreamCloseOutcome(t, ev)
		if _, ok := oc.(*genv1.StreamClose_Success); !ok {
			t.Fatalf("expected Success, got %T", oc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}

func TestReceiver_HandleThenRegister(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-hr"
	postCallback(t, r.URL(), ackID, map[string]any{
		"success": map[string]any{"changed": false},
	})

	time.Sleep(100 * time.Millisecond)

	ch := r.Register(ackID)
	select {
	case ev := <-ch:
		oc := extractStreamCloseOutcome(t, ev)
		if _, ok := oc.(*genv1.StreamClose_Success); !ok {
			t.Fatalf("expected Success, got %T", oc)
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
		"success": map[string]any{"changed": true},
	})
	postCallback(t, r.URL(), ackID, map[string]any{
		"error": map[string]any{"error_class": "later"},
	})

	first := <-ch
	oc := extractStreamCloseOutcome(t, first)
	if _, ok := oc.(*genv1.StreamClose_Success); !ok {
		t.Fatalf("expected first=Success, got %T", oc)
	}

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
			if i%2 == 0 {
				ch := r.Register(ackID)
				postCallback(t, r.URL(), ackID, map[string]any{
					"success": map[string]any{"changed": false},
				})
				select {
				case <-ch:
				case <-time.After(2 * time.Second):
					t.Errorf("ack=%s: timed out", ackID)
				}
			} else {
				postCallback(t, r.URL(), ackID, map[string]any{
					"success": map[string]any{"changed": true},
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

func TestReceiver_HandleRejectsMultiOutcome(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	body, _ := json.Marshal(map[string]any{
		"success": map[string]any{},
		"error":   map[string]any{"error_class": "x"},
	})
	resp, err := http.Post(r.URL()+"/v1/callback/x", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for multi-outcome body", resp.StatusCode)
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
