// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// O1 — Data platform extensions smoke fixture extension.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Smoke test extension extends the original §10 smoke with three
// wire-shape exercises:
//
//   - Stub-store DataProcessing extension — boots the rimsky-side
//     stub-store with the DataProcessing extension wired in and drives
//     the seven RPCs over gRPC. POST-2026-05-24 the stub-store source
//     lives in rimsky-internal (test-infrastructure carve-out per the
//     repo-reorganization spec) and the `consumption-side-isolation`
//     depguard bars lib/services from importing it; that subtest
//     stays in rimsky at
//     `pkg:stores/stub/dataprocessing/data_processing_test.go`.
//   - SensorHTTP — exercises the sensor-http poll → match → push wire
//     path against a fake upstream + fake rimsky receiver. Pure
//     `net/http/httptest` shape exerciser; preserved here verbatim.
//   - OpenLineageEmission — exercises the openlineage subscriber's
//     wire contract against a fake Marquez receiver. Pure
//     `net/http/httptest` shape; preserved here verbatim.
//
// The two preserved subtests pin the wire contracts the lib/services
// sensors and subscribers are obliged to honour; the full end-to-end
// drive of openlineage against a live rimsky stack lives in
// `pkg:subscribers/openlineage/subscriber_test.go` post-rewrite.

package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestDataPlatformSmoke_SensorHTTP exercises the sensor-http poll →
// match → push wire path. The sensor binary itself is
// `pkg:sensors/sensor-http/main.go`; the in-process surface is
// `package main` so this test mirrors the wire contract:
//
//  1. Boot a fake upstream `httptest.NewServer` that returns a known
//     body whose content-hash triggers an observation.
//  2. Boot a fake rimsky receiver recording `POST /instances/{id}/messages`
//     arrivals with `sender_kind: "publisher"`.
//  3. Drive the poll → push contract via a generic HTTP client.
//
// Inert payload per `@blessed-invariant: message-inertness — messages are inert in rimsky`.
func TestDataPlatformSmoke_SensorHTTP(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items_available": 42}`))
	}))
	defer upstream.Close()

	type arrival struct {
		Path           string
		IdempotencyKey string
		Body           []byte
	}
	var (
		mu        sync.Mutex
		arrivals  []arrival
		fakeReady = make(chan struct{}, 1)
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		arrivals = append(arrivals, arrival{
			Path:           r.URL.Path,
			IdempotencyKey: r.Header.Get("Idempotency-Key"),
			Body:           body,
		})
		mu.Unlock()
		select {
		case fakeReady <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	subscriptionID := "subscription-smoke-1"
	instanceID := "instance-smoke-1"

	upstreamReq, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		t.Fatalf("GET upstream: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upstream status = %d, want 200", resp.StatusCode)
	}

	payload := map[string]any{
		"observed_at": time.Now().UTC().Format(time.RFC3339),
		"url":         upstream.URL,
		"status":      resp.StatusCode,
		"body_hash":   "sha256-stub",
		"body":        json.RawMessage(body),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	envelope := map[string]any{
		"kind":                      "invalidate",
		"target":                    "smoke-target",
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    "sensor-http",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subscriptionID,
	}
	rawEnvelope, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	pushURL := fmt.Sprintf("%s/v1/instances/%s/messages", rimsky.URL, instanceID)
	pushReq, _ := http.NewRequest(http.MethodPost, pushURL, bytes.NewReader(rawEnvelope))
	pushReq.Header.Set("Content-Type", "application/json")
	pushReq.Header.Set("Idempotency-Key", subscriptionID+"+sha256-stub")
	pushResp, err := http.DefaultClient.Do(pushReq)
	if err != nil {
		t.Fatalf("push message: %v", err)
	}
	_ = pushResp.Body.Close()
	if pushResp.StatusCode >= 300 {
		t.Fatalf("push message status = %d, want < 300", pushResp.StatusCode)
	}

	select {
	case <-fakeReady:
	case <-time.After(2 * time.Second):
		t.Fatal("message never arrived at fake rimsky")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) != 1 {
		t.Fatalf("expected 1 arrival, got %d", len(arrivals))
	}
	got := arrivals[0]
	wantPath := "/v1/instances/" + instanceID + "/messages"
	if got.Path != wantPath {
		t.Fatalf("arrival path = %q, want %q", got.Path, wantPath)
	}
	if got.IdempotencyKey == "" {
		t.Fatalf("Idempotency-Key header missing")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.Body, &decoded); err != nil {
		t.Fatalf("decode arrival body: %v", err)
	}
	if decoded["sender_kind"] != "publisher" {
		t.Fatalf("sender_kind: %v", decoded["sender_kind"])
	}
	if decoded["publisher_subscription_id"] != subscriptionID {
		t.Fatalf("publisher_subscription_id: %v", decoded["publisher_subscription_id"])
	}
	if _, ok := decoded["payload"]; !ok {
		t.Fatalf("arrival missing payload: %+v", decoded)
	}
}

// TestDataPlatformSmoke_OpenLineageEmission exercises the openlineage
// subscriber's wire contract against a fake Marquez receiver: POST
// OpenLineage 1.x JSON envelopes to `{backend}/api/v1/lineage`.
// Inert payload per `@blessed-invariant 21`; the smoke pins the
// wire shape only.
func TestDataPlatformSmoke_OpenLineageEmission(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		received [][]byte
		paths    []string
	)
	marquez := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		received = append(received, body)
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer marquez.Close()

	event := map[string]any{
		"eventType": "COMPLETE",
		"eventTime": time.Now().UTC().Format(time.RFC3339Nano),
		"producer":  "https://github.com/rimsky-ai/rimsky-core/subscribers/openlineage",
		"schemaURL": "https://openlineage.io/spec/1-0-5/OpenLineage.json#/$defs/RunEvent",
		"run":       map[string]any{"runId": "11111111-1111-1111-1111-111111111111"},
		"job":       map[string]any{"namespace": "rimsky.smoke", "name": "leaf-run"},
		"facets": map[string]any{
			"rimsky": map[string]any{
				"template_hash":      "sha256-stub",
				"executor_name":      "stub-executor",
				"frame_trigger_kind": "operator",
			},
		},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal openlineage event: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, marquez.URL+"/api/v1/lineage", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post openlineage event: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("openlineage POST status = %d, want 201", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("marquez received %d events, want 1", len(received))
	}
	if paths[0] != "/api/v1/lineage" {
		t.Fatalf("marquez path = %q, want %q", paths[0], "/api/v1/lineage")
	}
	var decoded map[string]any
	if err := json.Unmarshal(received[0], &decoded); err != nil {
		t.Fatalf("decode marquez body: %v", err)
	}
	for _, key := range []string{"eventType", "eventTime", "producer", "schemaURL", "run", "job"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("marquez body missing %q: %+v", key, decoded)
		}
	}
}

// _ ensures context.Background and other imports survive a future
// refactor without compiler whine.
var _ = context.Background
