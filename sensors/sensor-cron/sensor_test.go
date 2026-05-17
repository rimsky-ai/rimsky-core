// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

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

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
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
	if len(caps.Protocols) != 1 || caps.Protocols[0] != "sensor" {
		t.Errorf("protocols: %+v", caps.Protocols)
	}
}

func TestStartWatch_ParsesAndComputesNextFire(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	// Pin clock so the next fire is deterministic.
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	_, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId:        "w1",
		InstanceId:     "i1",
		Kind:           "cron",
		ResolvedConfig: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	w, ok := s.watches["w1"]
	s.mu.Unlock()
	if !ok {
		t.Fatal("watch not registered")
	}
	if !w.NextFireAt.Equal(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)) {
		t.Errorf("next_fire_at: %s", w.NextFireAt)
	}
}

func TestStartWatch_RejectsInvalidCron(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	cfg := map[string]any{"cron": "not a cron"}
	raw, _ := json.Marshal(cfg)
	_, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected error for bad cron")
	}
}

func TestStartWatch_RejectsWrongKind(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	_, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{Kind: "http"})
	if err == nil {
		t.Fatal("expected error for non-cron kind")
	}
}

func TestStopWatch_IsIdempotent(t *testing.T) {
	s := NewSensorService("", noopLogger{})
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
	s := NewSensorService("", noopLogger{})
	s.mu.Lock()
	s.watches["w1"] = &Watch{WatchID: "w1", InstanceID: "i1", StartedAt: time.Now()}
	s.watches["w2"] = &Watch{WatchID: "w2", InstanceID: "i2", StartedAt: time.Now()}
	s.mu.Unlock()
	resp, err := s.ListWatches(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Watches) != 2 {
		t.Errorf("watches: %+v", resp.Watches)
	}
}

func TestTick_FiresDueWatchAndAdvances(t *testing.T) {
	var (
		mu       sync.Mutex
		observed int
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sensors/w1/observations" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body["cron"] != "*/5 * * * *" {
			t.Errorf("body.cron: %v", body["cron"])
		}
		mu.Lock()
		observed++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	// Advance clock past next_fire_at.
	s.clock = func() time.Time { return pin.Add(6 * time.Minute) }
	s.Tick(context.Background())
	mu.Lock()
	if observed != 1 {
		t.Errorf("observations: %d", observed)
	}
	mu.Unlock()

	// Next fire should be 00:10 (advanced from 00:05, not now).
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
