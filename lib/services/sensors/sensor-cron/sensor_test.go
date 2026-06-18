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
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type noopLogger struct{}

func (noopLogger) Info(_ string, _ ...any)  {}
func (noopLogger) Warn(_ string, _ ...any)  {}
func (noopLogger) Error(_ string, _ ...any) {}

func TestCapabilities_AdvertiseCron(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.SupportedKinds) != 1 || caps.SupportedKinds[0].Kind != "cron" {
		t.Errorf("kinds: %+v", caps.SupportedKinds)
	}
	if len(caps.Protocols) != 1 || caps.Protocols[0] != "publisher" {
		t.Errorf("protocols: %+v", caps.Protocols)
	}
}

func TestSubscribe_ParsesAndComputesNextFire(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1",
		InstanceId:              "i1",
		Kind:                    "cron",
		ResolvedConfig:          raw,
		TargetNode:              "tick",
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
	if !w.NextFireAt.Equal(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)) {
		t.Errorf("next_fire_at: %s", w.NextFireAt)
	}
	if w.TargetNode != "tick" || w.MessageType != "invalidate" {
		t.Errorf("routing fields: %+v", w)
	}
}

func TestSubscribe_RejectsInvalidCron(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	cfg := map[string]any{"cron": "not a cron"}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected error for bad cron")
	}
}

func TestSubscribe_RejectsWrongKind(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{Kind: "http"})
	if err == nil {
		t.Fatal("expected error for non-cron kind")
	}
}

func TestUnsubscribe_IsIdempotent(t *testing.T) {
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

func TestListSubscriptions(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	s.mu.Lock()
	s.watches["w1"] = &Watch{SubscriptionID: "w1", InstanceID: "i1", StartedAt: time.Now()}
	s.watches["w2"] = &Watch{SubscriptionID: "w2", InstanceID: "i2", StartedAt: time.Now()}
	s.mu.Unlock()
	resp, err := s.ListSubscriptions(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Subscriptions) != 2 {
		t.Errorf("subscriptions: %+v", resp.Subscriptions)
	}
}

func TestTick_FiresDueSubscriptionAndAdvances(t *testing.T) {
	var (
		mu       sync.Mutex
		observed int
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances/i1/messages" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if got := r.Header.Get("Idempotency-Key"); got == "" {
			t.Errorf("expected Idempotency-Key header")
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body["sender_kind"] != "publisher" {
			t.Errorf("body.sender_kind: %v", body["sender_kind"])
		}
		if body["publisher_subscription_id"] != "w1" {
			t.Errorf("body.publisher_subscription_id: %v", body["publisher_subscription_id"])
		}
		if body["type"] != "system/invalidate" {
			t.Errorf("body.type: %v", body["type"])
		}
		if _, present := body["target"]; present {
			t.Errorf("body.target unexpectedly present: %v", body["target"])
		}
		mu.Lock()
		observed++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
		TargetNode: "tick", MessageType: "system/invalidate",
	}); err != nil {
		t.Fatal(err)
	}
	s.clock = func() time.Time { return pin.Add(6 * time.Minute) }
	s.Tick(context.Background())
	mu.Lock()
	if observed != 1 {
		t.Errorf("messages observed: %d", observed)
	}
	mu.Unlock()

	s.mu.Lock()
	w := s.watches["w1"]
	s.mu.Unlock()
	want := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	if !w.NextFireAt.Equal(want) {
		t.Errorf("next_fire_at after tick: %s want %s", w.NextFireAt, want)
	}
	if w.LastFireAt == nil || !w.LastFireAt.Equal(pin.Add(6*time.Minute)) {
		t.Errorf("last_fire_at: %v", w.LastFireAt)
	}
}
