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

		MessageType: "invalidate",
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
	if w.MessageType != "invalidate" {
		t.Errorf("routing fields: %+v", w)
	}
}

func TestSubscribeThenListSubscriptions_RoundTripsResolvedConfig(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	raw, _ := json.Marshal(map[string]any{"cron": "*/5 * * * *"})
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1",
		InstanceId:              "i1",
		Kind:                    "cron",
		ResolvedConfig:          raw,
		MessageType:             "invalidate",
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := s.ListSubscriptions(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Subscriptions) != 1 {
		t.Fatalf("subscriptions: %+v", resp.Subscriptions)
	}
	if got := resp.Subscriptions[0].GetResolvedConfig(); !bytes.Equal(got, raw) {
		t.Errorf("resolved_config=%s, want %s", got, raw)
	}
	if resp.Subscriptions[0].GetMessageType() != "invalidate" {
		t.Errorf("message_type=%q, want invalidate", resp.Subscriptions[0].GetMessageType())
	}
	if resp.Subscriptions[0].GetStartedAt() == nil {
		t.Error("started_at not set")
	}
}

func TestSubscribe_NeverFiringCronDoesNotSetZeroFireTime(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	cfg := map[string]any{"cron": "0 0 31 2 *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	w := s.watches["w1"]
	s.mu.Unlock()
	if !w.NextFireAt.IsZero() {
		t.Fatalf("next_fire_at: %s (want zero time for a schedule that never fires)", w.NextFireAt)
	}
}

func TestTick_NeverFiringCronDoesNotFireOrLoop(t *testing.T) {
	var observed int32
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&observed, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"cron": "0 0 31 2 *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		s.clock = func() time.Time { return pin.Add(time.Duration(i) * 10 * 365 * 24 * time.Hour) }
		s.Tick(context.Background())
	}
	if got := atomic.LoadInt32(&observed); got != 0 {
		t.Errorf("messages observed for a never-firing cron: %d (want 0)", got)
	}
	s.mu.Lock()
	w := s.watches["w1"]
	s.mu.Unlock()
	if !w.NextFireAt.IsZero() {
		t.Errorf("next_fire_at after ticking: %s (want it to stay zero, not become permanently due)", w.NextFireAt)
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
		if got, want := r.Header.Get("Idempotency-Key"), "w1+2026-01-01T00:05:00Z"; got != want {
			t.Errorf("Idempotency-Key = %q, want %q (sub id + fire-window ISO)", got, want)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
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
		MessageType: "system/invalidate",
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

func TestTick_LongDowntimeCoalescesMissedWindowsIntoOneMessage(t *testing.T) {
	var (
		mu       sync.Mutex
		observed int
		bodies   []map[string]any
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		observed++
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"cron": "* * * * *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
		MessageType: "system/invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	downtime := pin.Add(3 * time.Hour)
	s.clock = func() time.Time { return downtime }
	s.Tick(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if observed != 1 {
		t.Fatalf("messages observed after a 3h outage on a minutely cron: got %d, want 1 "+
			"(catch-up must coalesce, not replay every missed window)", observed)
	}

	s.mu.Lock()
	w := s.watches["w1"]
	s.mu.Unlock()
	if !w.NextFireAt.After(downtime) {
		t.Fatalf("next_fire_at after coalesced catch-up: %s, want a window after %s", w.NextFireAt, downtime)
	}

	payload, _ := bodies[0]["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("envelope payload missing/wrong shape: %+v", bodies[0])
	}
	missed, _ := payload["missed_windows"].(float64)
	if missed < 100 {
		t.Errorf("payload.missed_windows: got %v, want a large count reflecting the 3h backlog", missed)
	}
}

func TestSubscribe_EvaluatesCronExpressionInUTCRegardlessOfProcessLocation(t *testing.T) {
	notUTC := time.FixedZone("UTC-7", -7*60*60)
	s := NewSensorService("", noopLogger{})
	pin := time.Date(2026, 1, 1, 12, 0, 0, 0, notUTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"cron": "0 0 * * *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
		MessageType: "system/invalidate",
	}); err != nil {
		t.Fatal(err)
	}
	w := s.watches["w1"]
	want := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if !w.NextFireAt.Equal(want) {
		t.Fatalf("next_fire_at: got %s, want %s (cron must evaluate in UTC, not the process's local zone)",
			w.NextFireAt, want)
	}
	if w.NextFireAt.Location() != time.UTC {
		t.Errorf("next_fire_at location: got %s, want UTC", w.NextFireAt.Location())
	}
}

func TestTick_PermanentRejectionDropsFire_AdvancesNextFireWindow(t *testing.T) {
	var attempts int32
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	raw, _ := json.Marshal(map[string]any{"cron": "*/5 * * * *"})
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
		MessageType: "system/invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	fireAt := pin.Add(6 * time.Minute)
	s.clock = func() time.Time { return fireAt }
	s.Tick(context.Background())
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts after rejected tick: %d (want 1; permanent 4xx must not be retried within Send)", got)
	}

	s.mu.Lock()
	w := s.watches["w1"]
	nextFireAt := w.NextFireAt
	lastFireAt := w.LastFireAt
	s.mu.Unlock()
	wantNext := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	if !nextFireAt.Equal(wantNext) {
		t.Fatalf("next_fire_at after permanent rejection: %s, want %s — a permanently rejected "+
			"fire is consumed, so the window cursor must advance exactly as on success", nextFireAt, wantNext)
	}
	if lastFireAt == nil || !lastFireAt.Equal(fireAt) {
		t.Fatalf("last_fire_at after permanent rejection: %v, want %s", lastFireAt, fireAt)
	}

	s.clock = func() time.Time { return pin.Add(7 * time.Minute) }
	s.Tick(context.Background())
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts after next tick before the next window: %d (want still 1; "+
			"the dropped fire must not be re-attempted)", got)
	}
}

func TestTick_FailedEmissionDoesNotAdvanceState_NextTickRetriesSameObservation(t *testing.T) {
	var (
		mu         sync.Mutex
		rimskyFail = true
		observed   int
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		fail := rimskyFail
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
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
		MessageType: "system/invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	s.clock = func() time.Time { return pin.Add(6 * time.Minute) }
	s.Tick(context.Background())
	mu.Lock()
	if observed != 0 {
		t.Fatalf("observed after failing tick: %d (want 0)", observed)
	}
	mu.Unlock()

	s.mu.Lock()
	nextFireAfterFailure := s.watches["w1"].NextFireAt
	lastFireAfterFailure := s.watches["w1"].LastFireAt
	s.mu.Unlock()
	wantNextFireAt := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	if !nextFireAfterFailure.Equal(wantNextFireAt) {
		t.Fatalf("next_fire_at advanced to %s despite the emission failing — "+
			"a failed POST must not move the fire-window cursor, or the next tick "+
			"will treat this fire window as already-observed and never retry",
			nextFireAfterFailure)
	}
	if lastFireAfterFailure != nil {
		t.Fatalf("last_fire_at set to %v despite the emission failing", lastFireAfterFailure)
	}

	mu.Lock()
	rimskyFail = false
	mu.Unlock()

	s.clock = func() time.Time { return pin.Add(7 * time.Minute) }
	s.Tick(context.Background())
	mu.Lock()
	if observed != 1 {
		t.Fatalf("observed after retry tick (rimsky now healthy): %d (want 1) — "+
			"the same fire window must be retried, not silently skipped, because the prior "+
			"tick never advanced next_fire_at", observed)
	}
	mu.Unlock()

	s.mu.Lock()
	w := s.watches["w1"]
	s.mu.Unlock()
	wantNextFireAfterSuccess := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	if !w.NextFireAt.Equal(wantNextFireAfterSuccess) {
		t.Fatalf("next_fire_at after successful retry: %s want %s", w.NextFireAt, wantNextFireAfterSuccess)
	}
	if w.LastFireAt == nil || !w.LastFireAt.Equal(pin.Add(7*time.Minute)) {
		t.Fatalf("last_fire_at after successful retry: %v", w.LastFireAt)
	}

	s.clock = func() time.Time { return pin.Add(8 * time.Minute) }
	s.Tick(context.Background())
	mu.Lock()
	if observed != 1 {
		t.Fatalf("observed after a tick before the next fire window: %d (want still 1)", observed)
	}
	mu.Unlock()
}
