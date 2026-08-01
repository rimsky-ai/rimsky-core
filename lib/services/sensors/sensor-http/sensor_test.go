// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"
)

type noopLogger struct{}

func (noopLogger) Info(_ string, _ ...any)  {}
func (noopLogger) Warn(_ string, _ ...any)  {}
func (noopLogger) Error(_ string, _ ...any) {}

func loopbackGuard(t *testing.T) egress.Guard {
	t.Helper()
	g, err := egress.NewGuard([]string{"127.0.0.0/8", "::1/128"})
	if err != nil {
		t.Fatalf("build loopback egress guard: %v", err)
	}
	return g
}

func TestCapabilities_AdvertiseHTTP(t *testing.T) {
	s := NewSensorService("", loopbackGuard(t), noopLogger{})
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
	s := NewSensorService("", loopbackGuard(t), noopLogger{})
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
	if w.URL != "http://example.test/feed.json" {
		t.Errorf("url: %s", w.URL)
	}
	if w.PollInterval != 15*time.Second {
		t.Errorf("interval: %s", w.PollInterval)
	}
	if w.MessageType != "invalidate" {
		t.Errorf("routing fields: %+v", w)
	}
}

func TestSubscribeThenListSubscriptions_RoundTripsResolvedConfig(t *testing.T) {
	s := NewSensorService("", loopbackGuard(t), noopLogger{})
	raw, _ := json.Marshal(map[string]any{"url": "http://example.test/feed.json", "poll_interval": "15s"})
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1",
		InstanceId:              "i1",
		Kind:                    "http",
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
	if got := resp.Subscriptions[0].GetResolvedConfig(); string(got) != string(raw) {
		t.Errorf("resolved_config=%s, want %s", got, raw)
	}
}

func TestSubscribe_RejectsMissingURL(t *testing.T) {
	s := NewSensorService("", loopbackGuard(t), noopLogger{})
	cfg := map[string]any{"poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "http", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestSubscribe_RejectsNonPositivePollInterval(t *testing.T) {
	for _, interval := range []string{"0s", "-5s"} {
		s := NewSensorService("", loopbackGuard(t), noopLogger{})
		cfg := map[string]any{"url": "http://example.test/feed.json", "poll_interval": interval}
		raw, _ := json.Marshal(cfg)
		_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
			PublisherSubscriptionId: "w1", Kind: "http", ResolvedConfig: raw,
		})
		if err == nil {
			t.Errorf("poll_interval %q: expected rejection, got success (would hot-poll every tick)", interval)
		}
	}
}

func TestListSubscriptions_StartedAtIsSubscribeTimeNotCallTime(t *testing.T) {
	s := NewSensorService("", loopbackGuard(t), noopLogger{})
	subscribeTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return subscribeTime }
	raw, _ := json.Marshal(map[string]any{"url": "http://example.test/feed.json"})
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.clock = func() time.Time { return subscribeTime.Add(time.Hour) }
	resp, err := s.ListSubscriptions(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Subscriptions) != 1 {
		t.Fatalf("subscriptions: %+v", resp.Subscriptions)
	}
	if got := resp.Subscriptions[0].GetStartedAt().AsTime(); !got.Equal(subscribeTime) {
		t.Errorf("started_at: got %s, want %s (must be the original subscribe time, "+
			"not the ListSubscriptions call time)", got, subscribeTime)
	}
}

func TestSubscribe_RejectsWrongKind(t *testing.T) {
	s := NewSensorService("", loopbackGuard(t), noopLogger{})
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{Kind: "cron"})
	if err == nil {
		t.Fatal("expected error for non-http kind")
	}
}

func TestUnsubscribeIdempotent(t *testing.T) {
	s := NewSensorService("", loopbackGuard(t), noopLogger{})
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
		obsIdem []string
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
		obsIdem = append(obsIdem, r.Header.Get("Idempotency-Key"))
		obsMu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"url": upstream.URL, "poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 1 {
		t.Fatalf("messages after first tick: %d", len(obsBody))
	}
	if sub, _ := obsBody[0]["publisher_subscription_id"].(string); sub == "" {
		t.Errorf("publisher_subscription_id: missing or empty (auth path discriminator)")
	}
	wantIdem := func(body string, at time.Time) string {
		sum := sha256.Sum256([]byte(body))
		return fmt.Sprintf("w1+%s+%d", hex.EncodeToString(sum[:]), at.UnixNano())
	}
	if got, want := obsIdem[0], wantIdem(`{"status":"ready","version":1}`, pin); got != want {
		t.Errorf("Idempotency-Key = %q, want %q (sub id + body sha-256 + observation nanotime)", got, want)
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
	changedAt := pin.Add(30 * time.Second)
	s.clock = func() time.Time { return changedAt }
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 2 {
		t.Errorf("messages after change: %d (want 2)", len(obsBody))
	}
	if got, want := obsIdem[1], wantIdem(`{"status":"ready","version":2}`, changedAt); got != want {
		t.Errorf("Idempotency-Key after change = %q, want %q (sub id + new body sha-256 + observation nanotime)", got, want)
	}
	obsMu.Unlock()
}

func TestTick_RevertedBodyGetsDistinctIdempotencyKeyFromEarlierObservation(t *testing.T) {
	var (
		target  atomic.Value
		obsMu   sync.Mutex
		obsIdem []string
	)
	target.Store(`{"status":"A"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := target.Load().(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		obsMu.Lock()
		obsIdem = append(obsIdem, r.Header.Get("Idempotency-Key"))
		obsMu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"url": upstream.URL, "poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	s.Tick(context.Background())

	target.Store(`{"status":"B"}`)
	s.clock = func() time.Time { return pin.Add(10 * time.Second) }
	s.Tick(context.Background())

	target.Store(`{"status":"A"}`)
	s.clock = func() time.Time { return pin.Add(20 * time.Second) }
	s.Tick(context.Background())

	obsMu.Lock()
	defer obsMu.Unlock()
	if len(obsIdem) != 3 {
		t.Fatalf("messages observed for A->B->A: %d (want 3)", len(obsIdem))
	}
	if obsIdem[0] == obsIdem[2] {
		t.Errorf("idempotency key for the reverted-to-A observation collided with the original A observation: %q == %q; "+
			"the durable message-idempotency table would silently drop this legitimate change-back", obsIdem[2], obsIdem[0])
	}
}

func TestTick_PollClientEnforcesEgressGuard(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}))
	defer upstream.Close()

	pushed := 0
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, egress.Guard{}, noopLogger{})
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
		t.Errorf("egress guard must block the loopback poll target; pushed=%d (want 0)", pushed)
	}
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

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
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

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
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

func TestTick_JSONPathFilter_SubstringIsNotAMatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deployment":{"status":"unhealthy"}}`))
	}))
	defer upstream.Close()

	pushed := 0
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
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
	if pushed != 0 {
		t.Errorf("pushed: %d (want 0; \"unhealthy\" must not match filter value \"healthy\" via substring)", pushed)
	}
}

func TestTick_OversizedResponseBody_RejectedNotBuffered(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("a"), int(maxPollBodyBytes+1)))
	}))
	defer upstream.Close()

	pushed := 0
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	raw, _ := json.Marshal(map[string]any{"url": upstream.URL, "poll_interval": "10s"})
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.Tick(context.Background())
	if pushed != 0 {
		t.Errorf("pushed: %d (want 0; oversized body must be rejected, not delivered)", pushed)
	}
}

func TestTick_PollsDueWatchesConcurrently_OneSlowWatchDoesNotBlockAnother(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fast.Close()

	fastPolled := make(chan struct{})
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	for id, url := range map[string]string{"slow": slow.URL, "fast": fast.URL} {
		raw, _ := json.Marshal(map[string]any{"url": url, "poll_interval": "1ms"})
		if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
			PublisherSubscriptionId: id, InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
		}); err != nil {
			t.Fatal(err)
		}
	}

	go func() {
		s.mu.Lock()
		w := s.watches["fast"]
		s.mu.Unlock()
		for {
			s.mu.Lock()
			polled := !w.LastPollAt.IsZero()
			s.mu.Unlock()
			if polled {
				close(fastPolled)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	tickDone := make(chan struct{})
	go func() {
		s.Tick(context.Background())
		close(tickDone)
	}()

	<-fastPolled
	close(release)
	<-tickDone
}

func TestTick_PermanentRejectionDropsObservation_AdvancesStateAndDoesNotReEmit(t *testing.T) {
	const upstreamBody = `{"status":"ready","version":1}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	var attempts int32
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	raw, _ := json.Marshal(map[string]any{"url": upstream.URL, "poll_interval": "10s"})
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	s.Tick(context.Background())
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts after rejected tick: %d (want 1; permanent 4xx must not be retried within Send)", got)
	}
	sum := sha256.Sum256([]byte(upstreamBody))
	wantHash := hex.EncodeToString(sum[:])
	s.mu.Lock()
	lastHash := s.watches["w1"].LastHash
	s.mu.Unlock()
	if lastHash != wantHash {
		t.Fatalf("LastHash after permanent rejection: %q, want %q — a permanently rejected "+
			"observation is consumed, so the dedup cursor must advance exactly as on success", lastHash, wantHash)
	}

	s.clock = func() time.Time { return pin.Add(15 * time.Second) }
	s.Tick(context.Background())
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts after next tick with unchanged body: %d (want still 1; "+
			"the dropped observation must not be re-emitted)", got)
	}
}

func TestTick_FailedEmissionDoesNotAdvanceState_NextTickRetriesSameObservation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready","version":1}`))
	}))
	defer upstream.Close()

	var (
		rimskyMu   sync.Mutex
		rimskyFail = true
		pushed     int
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rimskyMu.Lock()
		fail := rimskyFail
		rimskyMu.Unlock()
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"url": upstream.URL, "poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	s.Tick(context.Background())
	if pushed != 0 {
		t.Fatalf("pushed after exhausted-retry tick: %d (want 0)", pushed)
	}
	s.mu.Lock()
	lastHashAfterFailure := s.watches["w1"].LastHash
	s.mu.Unlock()
	if lastHashAfterFailure != "" {
		t.Fatalf("LastHash advanced to %q despite every emission attempt failing — "+
			"a real failure must not move the fire-window cursor, or the next tick "+
			"will treat the unchanged upstream body as already-observed and never retry",
			lastHashAfterFailure)
	}

	rimskyMu.Lock()
	rimskyFail = false
	rimskyMu.Unlock()

	s.clock = func() time.Time { return pin.Add(15 * time.Second) }
	s.Tick(context.Background())
	if pushed != 1 {
		t.Fatalf("pushed after retry tick (upstream body unchanged, rimsky now healthy): %d (want 1) — "+
			"the same observation must be retried, not silently dropped, because the prior "+
			"tick never advanced the fire-window cursor", pushed)
	}
	s.mu.Lock()
	lastHashAfterSuccess := s.watches["w1"].LastHash
	s.mu.Unlock()
	if lastHashAfterSuccess == "" {
		t.Fatal("LastHash must advance once the emission actually succeeds")
	}

	s.clock = func() time.Time { return pin.Add(30 * time.Second) }
	s.Tick(context.Background())
	if pushed != 1 {
		t.Fatalf("pushed after a tick with the same already-emitted body: %d (want still 1)", pushed)
	}
}
