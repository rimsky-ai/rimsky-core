// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

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
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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
	if len(caps.Protocols) != 1 || caps.Protocols[0] != "publisher" {
		t.Errorf("protocols: %+v", caps.Protocols)
	}
}

func TestSubscribe_MountsRouteAndForwards(t *testing.T) {
	var (
		obsMu   sync.Mutex
		obsBody []map[string]any
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/instances/") || !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("path: %s", r.URL.Path)
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

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/abc"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "webhook", ResolvedConfig: raw,
		TargetNode: "ingest", MessageType: "invalidate",
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
		t.Fatalf("messages: %d", len(obsBody))
	}
	body := obsBody[0]
	if body["sender_kind"] != "publisher" {
		t.Errorf("sender_kind: %v", body["sender_kind"])
	}
	// `target` is no longer on the envelope: the
	// `rimsky_messages.target` column was retired in migration 010 of
	// the 2026-06-14 message-schema-layer reshape, and the sensor no
	// longer sends it. Routing happens via the subscription's
	// target_node on rimsky's side, not via a wire envelope field.
	if _, present := body["target"]; present {
		t.Errorf("target unexpectedly present: %v", body["target"])
	}
	payload, ok := body["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload: %+v", body["payload"])
	}
	if payload["path"] != "/wh/abc" {
		t.Errorf("payload.path: %v", payload["path"])
	}
}

func TestSubscribe_NormalizesLeadingSlash(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	cfg := map[string]any{"path_prefix": "abc/123"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watches["w1"].PathPrefix != "/abc/123" {
		t.Errorf("path: %s", s.watches["w1"].PathPrefix)
	}
}

func TestSubscribe_RejectsBadKind(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{Kind: "cron"})
	if err == nil {
		t.Fatal("expected error for non-webhook kind")
	}
}

func TestIdempotencyHeader_Deduplicates(t *testing.T) {
	var pushed int
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"path_prefix": "/wh/idem", "idempotency_header": "X-Idem"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
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

func TestUnsubscribeIdempotent(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
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

func TestListSubscriptions(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s.mu.Lock()
	s.watches["w1"] = &Watch{SubscriptionID: "w1", InstanceID: "i1", StartedAt: s.clock()}
	s.watches["w2"] = &Watch{SubscriptionID: "w2", InstanceID: "i2", StartedAt: s.clock()}
	s.mu.Unlock()
	resp, err := s.ListSubscriptions(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Subscriptions) != 2 {
		t.Errorf("subscriptions: %+v", resp.Subscriptions)
	}
}
