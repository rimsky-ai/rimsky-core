// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestParseCallbackBody_Success(t *testing.T) {
	body := map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"k": "v"},
			"changed":          true,
			"change_summary":   "applied",
			"tags":             []any{"loop"},
		},
	}
	out, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	succ, ok := out.GetOutcome().(*genv1.Outcome_Success)
	if !ok {
		t.Fatalf("expected Success, got %T", out.GetOutcome())
	}
	if !succ.Success.GetChanged() {
		t.Errorf("changed not propagated")
	}
	if succ.Success.GetChangeSummary() != "applied" {
		t.Errorf("change_summary=%q want=applied", succ.Success.GetChangeSummary())
	}
	if got := succ.Success.GetAttributesDelta().AsMap(); got["k"] != "v" {
		t.Errorf("attributes_delta=%v want k=v", got)
	}
	tags := succ.Success.GetTags()
	if len(tags) != 1 || tags[0] != "loop" {
		t.Errorf("tags=%v want [loop]", tags)
	}
}

func TestParseCallbackBody_Error(t *testing.T) {
	body := map[string]any{
		"error": map[string]any{
			"error_class":      "boom",
			"payload":          map[string]any{"detail": "x"},
			"attributes_delta": map[string]any{"last_error": "boom"},
			"tags":             []any{"failed"},
		},
	}
	out, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	errOut, ok := out.GetOutcome().(*genv1.Outcome_Error)
	if !ok {
		t.Fatalf("expected Error, got %T", out.GetOutcome())
	}
	if errOut.Error.GetErrorClass() != "boom" {
		t.Errorf("error_class=%q want=boom", errOut.Error.GetErrorClass())
	}
	if got := errOut.Error.GetPayload().AsMap(); got["detail"] != "x" {
		t.Errorf("payload=%v want detail=x", got)
	}
	if got := errOut.Error.GetAttributesDelta().AsMap(); got["last_error"] != "boom" {
		t.Errorf("attributes_delta=%v want last_error=boom", got)
	}
	if tags := errOut.Error.GetTags(); len(tags) != 1 || tags[0] != "failed" {
		t.Errorf("tags=%v want [failed]", tags)
	}
}

func TestParseCallbackBody_Park(t *testing.T) {
	resumeAt := time.Date(2026, 6, 17, 15, 30, 0, 0, time.UTC).Format(time.RFC3339)
	body := map[string]any{
		"park": map[string]any{
			"reason":           "snooze",
			"reason_note":      "rate-limited until cooldown elapses",
			"reason_label":     "rate_limit",
			"resume_at":        resumeAt,
			"attributes_delta": map[string]any{"session_token": "tok-1"},
			"tags":             []any{"parked"},
		},
	}
	out, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	park, ok := out.GetOutcome().(*genv1.Outcome_Park)
	if !ok {
		t.Fatalf("expected Park, got %T", out.GetOutcome())
	}
	if park.Park.GetReason() != genv1.ParkReason_PARK_REASON_SNOOZE {
		t.Errorf("park reason=%v want=SNOOZE", park.Park.GetReason())
	}
	if park.Park.GetReasonNote() != "rate-limited until cooldown elapses" {
		t.Errorf("reason_note=%q", park.Park.GetReasonNote())
	}
	if park.Park.GetReasonLabel() != "rate_limit" {
		t.Errorf("reason_label=%q", park.Park.GetReasonLabel())
	}
	if park.Park.GetResumeAt() == nil {
		t.Fatal("resume_at not propagated")
	}
	if got := park.Park.GetResumeAt().AsTime(); !got.Equal(time.Date(2026, 6, 17, 15, 30, 0, 0, time.UTC)) {
		t.Errorf("resume_at=%v", got)
	}
	if tags := park.Park.GetTags(); len(tags) != 1 || tags[0] != "parked" {
		t.Errorf("tags=%v want [parked]", tags)
	}
}

func TestParseCallbackBody_ParkUnknownReasonFallsBackToAwaitCallback(t *testing.T) {
	body := map[string]any{
		"park": map[string]any{"reason": "made_up"},
	}
	out, err := parseCallbackBody(body)
	if err != nil {
		t.Fatalf("parseCallbackBody: %v", err)
	}
	park, ok := out.GetOutcome().(*genv1.Outcome_Park)
	if !ok {
		t.Fatalf("expected Park, got %T", out.GetOutcome())
	}
	if park.Park.GetReason() != genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK {
		t.Errorf("park reason=%v want=AWAIT_CALLBACK fallback", park.Park.GetReason())
	}
}

func TestParseCallbackBody_RejectsMultipleOutcomes(t *testing.T) {
	body := map[string]any{
		"success": map[string]any{},
		"error":   map[string]any{"error_class": "x"},
	}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for multi-outcome body")
	}
}

func TestParseCallbackBody_RejectsAllThreeOutcomes(t *testing.T) {
	body := map[string]any{
		"success": map[string]any{},
		"error":   map[string]any{"error_class": "x"},
		"park":    map[string]any{},
	}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for body with all three outcomes")
	}
}

func TestParseCallbackBody_RejectsNoOutcome(t *testing.T) {
	if _, err := parseCallbackBody(map[string]any{}); err == nil {
		t.Fatal("expected error for empty body")
	}
	if _, err := parseCallbackBody(map[string]any{"events": []any{}}); err == nil {
		t.Fatal("expected error for body with no outcome field")
	}
}

func TestParseCallbackBody_RejectsLegacyTypeDiscriminator(t *testing.T) {
	body := map[string]any{
		"type":             "complete",
		"attributes_delta": map[string]any{},
	}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for legacy type-discriminator body")
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
	case out := <-ch:
		if out == nil {
			t.Fatal("nil outcome")
		}
		if _, ok := out.GetOutcome().(*genv1.Outcome_Success); !ok {
			t.Fatalf("expected Success, got %T", out.GetOutcome())
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
	case out := <-ch:
		if _, ok := out.GetOutcome().(*genv1.Outcome_Success); !ok {
			t.Fatalf("expected Success, got %T", out.GetOutcome())
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
	if _, ok := first.GetOutcome().(*genv1.Outcome_Success); !ok {
		t.Fatalf("expected first=Success, got %T", first.GetOutcome())
	}

	select {
	case extra, ok := <-ch:
		if ok && extra != nil {
			t.Fatalf("unexpected duplicate delivered: %T", extra.GetOutcome())
		}
	case <-time.After(150 * time.Millisecond):
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
	if want := "127.0.0.1"; !bytes.Contains([]byte(r.URL()), []byte(want)) {
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

func TestReceiver_HandleRejectsEmptyOutcome(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	body, _ := json.Marshal(map[string]any{})
	resp, err := http.Post(r.URL()+"/v1/callback/x", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for empty outcome body", resp.StatusCode)
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

func TestReceiver_HandleRejectsMissingAckID(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	body, _ := json.Marshal(map[string]any{"success": map[string]any{}})
	resp, err := http.Post(r.URL()+"/v1/callback/", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for missing ack_id", resp.StatusCode)
	}
}

func TestReceiver_HandleSuccessReturns204(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	body, _ := json.Marshal(map[string]any{"success": map[string]any{"changed": true}})
	resp, err := http.Post(r.URL()+"/v1/callback/ack-204", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status=%d want 204", resp.StatusCode)
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
