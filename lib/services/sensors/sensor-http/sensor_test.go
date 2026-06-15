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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type noopLogger struct{}

func (noopLogger) Info(_ string, _ ...any)  {}
func (noopLogger) Warn(_ string, _ ...any)  {}
func (noopLogger) Error(_ string, _ ...any) {}

func TestCapabilities_AdvertiseHTTP(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.SupportedKinds) != 1 || caps.SupportedKinds[0].Kind != "http" {
		t.Errorf("kinds: %+v", caps.SupportedKinds)
	}
	if len(caps.Protocols) != 1 || caps.Protocols[0] != "publisher" {
		t.Errorf("protocols: %+v", caps.Protocols)
	}
}

func TestSubscribe_ParsesAndRegisters(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	cfg := map[string]any{
		"url":           "http://example.test/feed.json",
		"poll_interval": "15s",
	}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1",
		InstanceId:              "i1",
		Kind:                    "http",
		ResolvedConfig:          raw,
		TargetNode:              "feed",
		MessageType:             "invalidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	w, ok := s.watches["w1"]
	s.mu.Unlock()
	if !ok {
		t.Fatal("subscription not registered")
	}
	if w.URL != "http://example.test/feed.json" {
		t.Errorf("url: %s", w.URL)
	}
	if w.PollInterval != 15*time.Second {
		t.Errorf("interval: %s", w.PollInterval)
	}
	if w.TargetNode != "feed" || w.MessageType != "invalidate" {
		t.Errorf("routing fields: %+v", w)
	}
}

func TestSubscribe_RejectsMissingURL(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	cfg := map[string]any{"poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "http", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestSubscribe_RejectsWrongKind(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{Kind: "cron"})
	if err == nil {
		t.Fatal("expected error for non-http kind")
	}
}

func TestUnsubscribeIdempotent(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	s.mu.Lock()
	s.watches["w1"] = &Watch{SubscriptionID: "w1"}
	s.mu.Unlock()
	for i := 0; i < 2; i++ {
		if _, err := s.Unsubscribe(context.Background(), &genv1.UnsubscribeRequest{PublisherSubscriptionId: "w1"}); err != nil {
			t.Fatalf("unsubscribe[%d]: %v", i, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.watches) != 0 {
		t.Errorf("watches: %+v", s.watches)
	}
}

func TestTick_PollsAndPushesOnChange(t *testing.T) {
	var (
		target  atomic.Value
		obsMu   sync.Mutex
		obsBody []map[string]any
	)
	target.Store(`{"status":"ready","version":1}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := target.Load().(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/instances/") || !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got == "" {
			t.Errorf("expected Idempotency-Key header")
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		obsMu.Lock()
		obsBody = append(obsBody, body)
		obsMu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"url": upstream.URL, "poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
		TargetNode: "feed", MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 1 {
		t.Fatalf("messages after first tick: %d", len(obsBody))
	}
	if obsBody[0]["sender_kind"] != "publisher" {
		t.Errorf("sender_kind: %v", obsBody[0]["sender_kind"])
	}
	obsMu.Unlock()

	s.clock = func() time.Time { return pin.Add(15 * time.Second) }
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 1 {
		t.Errorf("messages after unchanged tick: %d (want 1)", len(obsBody))
	}
	obsMu.Unlock()

	target.Store(`{"status":"ready","version":2}`)
	s.clock = func() time.Time { return pin.Add(30 * time.Second) }
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 2 {
		t.Errorf("messages after change: %d (want 2)", len(obsBody))
	}
	obsMu.Unlock()
}

func TestTick_StatusFilter_RejectsNonMatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"err":1}`))
	}))
	defer upstream.Close()

	pushed := 0
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	cfg := map[string]any{"url": upstream.URL, "poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.Tick(context.Background())
	if pushed != 0 {
		t.Errorf("pushed: %d (want 0; 5xx should not match)", pushed)
	}
}

func TestTick_JSONPathFilter(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deployment":{"status":"healthy"}}`))
	}))
	defer upstream.Close()

	pushed := 0
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	cfg := map[string]any{
		"url":           upstream.URL,
		"poll_interval": "10s",
		"match": map[string]any{
			"jsonpath": map[string]any{
				"path":  "deployment.status",
				"value": "healthy",
			},
		},
	}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.Tick(context.Background())
	if pushed != 1 {
		t.Errorf("pushed: %d (want 1; jsonpath match)", pushed)
	}
}
