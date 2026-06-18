// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package lineage

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

type fakeEmitter struct {
	backend string
	client  *http.Client
}

func (f *fakeEmitter) Send(ctx context.Context, event map[string]any) error {
	if f.backend == "" {
		return nil
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.backend+"/api/v1/lineage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func TestOpenLineageEmission_PostsToBackend(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		received [][]byte
		paths    []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, body)
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	em := &fakeEmitter{backend: srv.URL, client: srv.Client()}
	event := map[string]any{
		"eventType": "COMPLETE",
		"job":       map[string]any{"namespace": "rimsky.test", "name": "leaf-run"},
		"run":       map[string]any{"runId": "11111111-1111-1111-1111-111111111111"},
	}
	if err := em.Send(context.Background(), event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 emission, got %d", len(received))
	}
	if paths[0] != "/api/v1/lineage" {
		t.Errorf("unexpected path: %q (want /api/v1/lineage)", paths[0])
	}
	var decoded map[string]any
	if err := json.Unmarshal(received[0], &decoded); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if decoded["eventType"] != "COMPLETE" {
		t.Errorf("eventType: got %v want COMPLETE", decoded["eventType"])
	}
}

func TestOpenLineageEmission_EmptyBackendIsNoOp(t *testing.T) {
	t.Parallel()
	em := &fakeEmitter{client: &http.Client{}}
	if err := em.Send(context.Background(), map[string]any{"eventType": "COMPLETE"}); err != nil {
		t.Errorf("empty backend should no-op, got %v", err)
	}
}
