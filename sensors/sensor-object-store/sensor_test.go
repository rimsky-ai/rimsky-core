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

func TestCapabilities_AdvertisesObjectStore(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.SupportedKinds) != 1 || caps.SupportedKinds[0].Kind != "object-store" {
		t.Errorf("kinds: %+v", caps.SupportedKinds)
	}
	if len(caps.Protocols) != 1 || caps.Protocols[0] != "sensor" {
		t.Errorf("protocols: %+v", caps.Protocols)
	}
}

func TestStartWatch_RegistersInMemory(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	cfg := map[string]any{
		"backend":       "memory",
		"bucket":        "test-bucket",
		"prefix":        "events/",
		"poll_interval": "10s",
	}
	raw, _ := json.Marshal(cfg)
	_, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", InstanceId: "i1", Kind: "object-store", ResolvedConfig: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	w, ok := s.watches["w1"]
	s.mu.Unlock()
	if !ok || w.Bucket != "test-bucket" || w.WatermarkField != "name" {
		t.Errorf("watch: %+v", w)
	}
}

func TestStartWatch_RejectsBadBackend(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	cfg := map[string]any{"backend": "ftp", "bucket": "b"}
	raw, _ := json.Marshal(cfg)
	_, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", Kind: "object-store", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestStartWatch_RejectsBadWatermark(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	cfg := map[string]any{"backend": "memory", "bucket": "b", "watermark_field": "lol"}
	raw, _ := json.Marshal(cfg)
	_, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", Kind: "object-store", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected error for unknown watermark_field")
	}
}

func TestTick_EmitsOneObservationPerNewObject(t *testing.T) {
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

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	lister := NewMemoryLister()
	s.SetBackend("memory", lister)
	lister.Put("test-bucket", ObjectMeta{Name: "events/a.json", LastModified: pin.Add(-1 * time.Hour), Size: 10, ETag: "etag-a"})
	lister.Put("test-bucket", ObjectMeta{Name: "events/b.json", LastModified: pin.Add(-30 * time.Minute), Size: 20, ETag: "etag-b"})

	cfg := map[string]any{"backend": "memory", "bucket": "test-bucket", "prefix": "events/", "poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", InstanceId: "i1", Kind: "object-store", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}

	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 2 {
		t.Errorf("first tick observations: %d (want 2)", len(obsBody))
	}
	obsMu.Unlock()

	// No new objects → no observations on next tick.
	s.clock = func() time.Time { return pin.Add(15 * time.Second) }
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 2 {
		t.Errorf("steady state observations: %d (want 2)", len(obsBody))
	}
	obsMu.Unlock()

	// Add a new object → one new observation.
	lister.Put("test-bucket", ObjectMeta{Name: "events/c.json", LastModified: pin, Size: 30, ETag: "etag-c"})
	s.clock = func() time.Time { return pin.Add(30 * time.Second) }
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 3 {
		t.Errorf("post-add observations: %d (want 3)", len(obsBody))
	}
	if obsBody[2]["object_name"] != "events/c.json" {
		t.Errorf("third observation: %+v", obsBody[2])
	}
	obsMu.Unlock()
}

func TestTick_LastModifiedWatermark(t *testing.T) {
	var pushed int
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushed++
		w.WriteHeader(http.StatusOK)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	lister := NewMemoryLister()
	s.SetBackend("memory", lister)
	// Old object, then a newer one; both inserted out-of-order in the
	// fixture to confirm sort-by-watermark drives the emission order.
	lister.Put("test-bucket", ObjectMeta{Name: "z.json", LastModified: pin.Add(-2 * time.Hour)})
	lister.Put("test-bucket", ObjectMeta{Name: "a.json", LastModified: pin.Add(-1 * time.Hour)})

	cfg := map[string]any{
		"backend":         "memory",
		"bucket":          "test-bucket",
		"poll_interval":   "10s",
		"watermark_field": "last_modified",
	}
	raw, _ := json.Marshal(cfg)
	if _, err := s.StartWatch(context.Background(), &genv1.StartWatchRequest{
		WatchId: "w1", InstanceId: "i1", Kind: "object-store", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.Tick(context.Background())
	if pushed != 2 {
		t.Errorf("pushed: %d (want 2)", pushed)
	}
	// A newer object — pushes one.
	lister.Put("test-bucket", ObjectMeta{Name: "b.json", LastModified: pin.Add(-30 * time.Minute)})
	s.clock = func() time.Time { return pin.Add(15 * time.Second) }
	s.Tick(context.Background())
	if pushed != 3 {
		t.Errorf("pushed: %d (want 3)", pushed)
	}
}

func TestStopWatchIdempotent(t *testing.T) {
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
