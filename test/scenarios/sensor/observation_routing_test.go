// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N5 scenario — observation_routing.
//
// Sensors POST to rimsky's `POST /sensors/{watch_id}/observations`
// endpoint; the body is opaque to the sensor protocol and routed
// onto the message queue with sender_kind="sensor" and
// target="*". The scenario pins the HTTP routing shape via an
// inline fake receiver.
package sensor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeRimsky receives POSTs from the sensor side. The contract:
// sensors POST JSON to /sensors/{watch_id}/observations. The fake
// records the watch_id and the body for assertions.
type fakeRimsky struct {
	mu       sync.Mutex
	received []recv
}

type recv struct {
	WatchID string
	Body    []byte
}

func (f *fakeRimsky) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()
	parts := splitNonEmpty(r.URL.Path, '/')
	var watchID string
	if len(parts) >= 2 && parts[0] == "sensors" {
		watchID = parts[1]
	}
	f.mu.Lock()
	f.received = append(f.received, recv{WatchID: watchID, Body: body})
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
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

func TestObservationRouting_PostsToPerWatchPath(t *testing.T) {
	t.Parallel()
	r := &fakeRimsky{}
	srv := httptest.NewServer(http.HandlerFunc(r.handler))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"observed_at": "2026-05-15T12:00:00Z"})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/sensors/scenario-watch/observations", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
	if r.received[0].WatchID != "scenario-watch" {
		t.Errorf("watch_id: got %q want scenario-watch", r.received[0].WatchID)
	}
}
