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
			"resume_at": resumeAt,
			"tags":      []any{"parked"},
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

func TestParseCallbackBody_RejectsNullOutcome(t *testing.T) {
	if _, err := parseCallbackBody(map[string]any{"success": nil}); err == nil {
		t.Fatal(`expected error for {"success": null}; a present-but-null outcome key must not count as a valid outcome`)
	}
}

func TestParseCallbackBody_RejectsNullOutcomeMixedWithLegacyField(t *testing.T) {
	body := map[string]any{"success": nil, "type": "complete"}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal(`expected error for {"success": null, "type": "complete"}`)
	}
}

func TestParseCallbackBody_RejectsUnknownTopLevelField(t *testing.T) {
	body := map[string]any{
		"success":          map[string]any{"changed": true},
		"unexpected_field": "x",
	}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for a body carrying an unrecognized top-level field alongside a valid outcome")
	}
}

func TestParseCallbackBody_RejectsReservedEventsField(t *testing.T) {
	body := map[string]any{
		"success": map[string]any{"changed": true},
		"events":  []any{},
	}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for a body carrying the reserved events field (executor.proto reserves it; no longer accepted)")
	}
}

func TestParseCallbackBody_ParkMissingResumeAtIsError(t *testing.T) {
	body := map[string]any{"park": map[string]any{"tags": []any{"parked"}}}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for a park outcome missing resume_at (required per executor.proto::Park)")
	}
}

func TestParseCallbackBody_ParkInvalidResumeAtIsError(t *testing.T) {
	body := map[string]any{"park": map[string]any{"resume_at": "not-a-timestamp"}}
	if _, err := parseCallbackBody(body); err == nil {
		t.Fatal("expected error for a park outcome with an unparseable resume_at, got PASS (silently dropping it is not conformant)")
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

func TestReceiver_DuplicateCallback_RejectedNotDelivered(t *testing.T) {
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

	ackStatus := postDuplicateCallback(t, r.URL(), ackID, map[string]any{
		"error": map[string]any{"error_class": "later"},
	})
	if ackStatus != "rejected_run_terminal" {
		t.Fatalf("duplicate callback ack_status=%q, want %q (matching the real supervisor's "+
			"rejected-ack_status shape for a re-delivered callback)", ackStatus, "rejected_run_terminal")
	}

	first := <-ch
	if _, ok := first.GetOutcome().(*genv1.Outcome_Success); !ok {
		t.Fatalf("expected first=Success, got %T", first.GetOutcome())
	}
	select {
	case extra := <-ch:
		t.Fatalf("duplicate callback must not be delivered to the registered channel, got: %T", extra.GetOutcome())
	default:
	}
}

func TestReceiver_LateCallback_AfterConsumptionRejectedNotDelivered(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-late"
	ch := r.Register(ackID)
	postCallback(t, r.URL(), ackID, map[string]any{
		"success": map[string]any{"changed": true},
	})
	<-ch

	ackStatus := postDuplicateCallback(t, r.URL(), ackID, map[string]any{
		"success": map[string]any{"changed": true},
	})
	if ackStatus != "rejected_run_terminal" {
		t.Fatalf("late callback ack_status=%q, want %q", ackStatus, "rejected_run_terminal")
	}
	select {
	case extra := <-ch:
		t.Fatalf("late callback must not be re-delivered to the registered channel, got: %T", extra.GetOutcome())
	default:
	}
}

func postDuplicateCallback(t *testing.T, baseURL, ackID string, body map[string]any) string {
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("duplicate/late callback status=%d, want 200", resp.StatusCode)
	}
	var ack map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		t.Fatalf("decode ack body: %v", err)
	}
	return ack["ack_status"]
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

func TestReceiver_SimulateRestart_RejectsAndSignalsAckID(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-restart"
	ch := r.Register(ackID)
	hits := r.SimulateRestart()

	buf, _ := json.Marshal(map[string]any{"success": map[string]any{"changed": true}})
	resp, err := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want %d during simulated restart", resp.StatusCode, http.StatusServiceUnavailable)
	}

	select {
	case gotID := <-hits:
		if gotID != ackID {
			t.Fatalf("restart-hit ackID=%q, want %q", gotID, ackID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the restart-simulation hit signal")
	}

	select {
	case <-ch:
		t.Fatal("the rejected callback must not resolve the registered channel")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestReceiver_EndSimulatedRestart_ResumesNormalDelivery(t *testing.T) {
	r, err := StartCallbackReceiver()
	if err != nil {
		t.Fatalf("StartCallbackReceiver: %v", err)
	}
	defer func() { _ = r.Close() }()

	ackID := "ack-restart-resume"
	ch := r.Register(ackID)
	hits := r.SimulateRestart()

	buf, _ := json.Marshal(map[string]any{"success": map[string]any{"changed": true}})
	resp, err := http.Post(r.URL()+"/v1/callback/"+ackID, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()

	<-hits
	r.EndSimulatedRestart()

	postCallback(t, r.URL(), ackID, map[string]any{"success": map[string]any{"changed": true}})

	select {
	case out := <-ch:
		if _, ok := out.GetOutcome().(*genv1.Outcome_Success); !ok {
			t.Fatalf("expected Success after EndSimulatedRestart, got %T", out.GetOutcome())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delivery after EndSimulatedRestart")
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
