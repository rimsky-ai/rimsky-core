// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

type noopLogger struct{}

func (noopLogger) Info(_ string, _ ...any)  {}
func (noopLogger) Warn(_ string, _ ...any)  {}
func (noopLogger) Error(_ string, _ ...any) {}

func TestCapabilities_AdvertisesWebhook(t *testing.T) {
	s := NewSensorService("", chi.NewRouter(), noopLogger{})
	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.SupportedKinds) != 1 || caps.SupportedKinds[0].Kind != "webhook" {
		t.Errorf("kinds: %+v", caps.SupportedKinds)
	}
}

func TestStartWatch_MountsRouteAndForwards(t *testing.T) {
	var (
		obsMu   sync.Mutex
		obsBody []map[string]any
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		obsMu.Lock()
		obsBody = append(obsBody, body)
		obsMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/abc"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", InstanceId: "i1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(router)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/wh/abc", "application/json", bytes.NewReader([]byte(`{"event":"created","id":42}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: %d", resp.StatusCode)
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	if len(obsBody) != 1 {
		t.Fatalf("observations: %d", len(obsBody))
	}
	body := obsBody[0]
	if body["path"] != "/wh/abc" {
		t.Errorf("path: %v", body["path"])
	}
	bm, ok := body["body"].(map[string]any)
	if !ok || bm["event"] != "created" {
		t.Errorf("body decoded: %+v", body["body"])
	}
}

func TestStartWatch_NormalizesLeadingSlash(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	cfg := map[string]any{"path_prefix": "abc/123"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watches["w1"].PathPrefix != "/abc/123" {
		t.Errorf("path: %s", s.watches["w1"].PathPrefix)
	}
}

func TestStartWatch_RejectsBadKind(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	_, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{Kind: "cron"})
	if err == nil {
		t.Fatal("expected error for non-webhook kind")
	}
}

func TestIdempotencyHeader_Deduplicates(t *testing.T) {
	var pushed int
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushed++
		w.WriteHeader(http.StatusOK)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"path_prefix": "/wh/idem", "idempotency_header": "X-Idem"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/idem", bytes.NewReader([]byte(`{"event":"x"}`)))
		req.Header.Set("X-Idem", "k1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if pushed != 1 {
		t.Errorf("pushed: %d (want 1; idempotency dedup)", pushed)
	}
	// Different idempotency value → push.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/idem", bytes.NewReader([]byte(`{"event":"y"}`)))
	req.Header.Set("X-Idem", "k2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if pushed != 2 {
		t.Errorf("pushed after new key: %d (want 2)", pushed)
	}
}

func TestStopWatchIdempotent(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	s.mu.Lock()
	s.watches["w1"] = &Watch{WatchID: "w1"}
	s.mu.Unlock()
	for i := 0; i < 2; i++ {
		if _, err := s.StopWatch(context.Background(), &genv1.StopWatchRequest{WatchId: "w1"}); err != nil {
			t.Fatalf("stop[%d]: %v", i, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.watches) != 0 {
		t.Errorf("watches: %+v", s.watches)
	}
}

func TestListWatches(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s.mu.Lock()
	s.watches["w1"] = &Watch{WatchID: "w1", InstanceID: "i1", StartedAt: s.clock()}
	s.watches["w2"] = &Watch{WatchID: "w2", InstanceID: "i2", StartedAt: s.clock()}
	s.mu.Unlock()
	resp, err := s.ListWatches(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Watches) != 2 {
		t.Errorf("watches: %+v", resp.Watches)
	}
}
