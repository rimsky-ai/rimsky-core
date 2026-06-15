// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// message_routing_test pins the publisher message-envelope shape that
// bundled sensors POST to rimsky's generic
// `POST /instances/{instance_id}/messages` endpoint. The envelope
// carries `sender_kind: "publisher"` + `publisher_subscription_id` so
// rimsky's capability check (the handler in
// `code:control/controlapi/messages.go::handleCreateMessage`) can
// validate the subscription exists and is active before insert.
//
// This scenario uses an inline fake receiver rather than a full rimsky
// stack to keep the test focused on the wire shape; the deeper
// capability-check unit tests live in
// `code:control/controlapi/messages_test.go`.
package sensor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeRimsky receives POSTs from the publisher side. The contract:
// publishers POST JSON envelopes to /instances/{instance_id}/messages
// with `sender_kind: "publisher"` and an Idempotency-Key header. The
// fake records what it sees for assertions.
type fakeRimsky struct {
	mu       sync.Mutex
	received []recv
}

type recv struct {
	InstanceID     string
	IdempotencyKey string
	Body           map[string]any
}

func (f *fakeRimsky) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()
	parts := splitNonEmpty(r.URL.Path, '/')
	var instanceID string
	if len(parts) >= 4 && parts[0] == "v1" && parts[1] == "instances" && parts[3] == "messages" {
		instanceID = parts[2]
	}
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)
	f.mu.Lock()
	f.received = append(f.received, recv{
		InstanceID:     instanceID,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Body:           decoded,
	})
	f.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

func splitNonEmpty(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func TestMessageRouting_PublisherPostsEnvelopeToInstanceMessages(t *testing.T) {
	t.Parallel()
	r := &fakeRimsky{}
	srv := httptest.NewServer(http.HandlerFunc(r.handler))
	defer srv.Close()

	envelope := map[string]any{
		"type":                      "sensor/observation",
		"payload":                   map[string]any{"observed_at": "2026-05-17T12:00:00Z"},
		"sender":                    "sensor-cron",
		"sender_kind":               "publisher",
		"publisher_subscription_id": "scenario-subscription",
	}
	body, _ := json.Marshal(envelope)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/v1/instances/scenario-instance/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "scenario-subscription+2026-05-17T12:00:00Z")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.received) != 1 {
		t.Fatalf("expected 1 received POST, got %d", len(r.received))
	}
	rec := r.received[0]
	if rec.InstanceID != "scenario-instance" {
		t.Errorf("instance_id: got %q want scenario-instance", rec.InstanceID)
	}
	if !strings.HasPrefix(rec.IdempotencyKey, "scenario-subscription") {
		t.Errorf("Idempotency-Key: got %q want prefix scenario-subscription", rec.IdempotencyKey)
	}
	if rec.Body["sender_kind"] != "publisher" {
		t.Errorf("body.sender_kind: %v", rec.Body["sender_kind"])
	}
	if rec.Body["publisher_subscription_id"] != "scenario-subscription" {
		t.Errorf("body.publisher_subscription_id: %v", rec.Body["publisher_subscription_id"])
	}
	if rec.Body["type"] != "sensor/observation" {
		t.Errorf("body.type: %v", rec.Body["type"])
	}
	// @constraint: `target` is no longer on the envelope — the
	// `rimsky_messages.target` column was retired by the message-
	// schema-layer reshape; routing happens via the subscription's
	// target_node on rimsky's side, not via a wire envelope field.
	if _, present := rec.Body["target"]; present {
		t.Errorf("body.target unexpectedly present: %v", rec.Body["target"])
	}
}
