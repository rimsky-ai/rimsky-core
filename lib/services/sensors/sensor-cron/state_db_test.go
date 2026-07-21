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
	"sync/atomic"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func newRecordingReceiver() (*httptest.Server, *int64, *[]map[string]any, *sync.Mutex) {
	var fireCount int64
	var bodies []map[string]any
	var bodiesMu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodiesMu.Lock()
		bodies = append(bodies, body)
		bodiesMu.Unlock()
		atomic.AddInt64(&fireCount, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	return srv, &fireCount, &bodies, &bodiesMu
}

func TestSensorCronStateDSN_SurvivesRestartAndFiresOnScheduledWindow(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_CRON_STATE_DSN", dsn)

	srv, fireCount, bodies, bodiesMu := newRecordingReceiver()
	defer srv.Close()

	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	state1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB (first): %v", err)
	}
	if state1 == nil {
		t.Fatal("openStateDB returned nil with DSN set — persistence not engaged")
	}
	s1 := NewSensorService(srv.URL, noopLogger{})
	s1.clock = func() time.Time { return registerTime }
	s1.AttachStateDB(state1)

	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s1.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "cron-1", InstanceId: "inst-1", Kind: "cron",
		ResolvedConfig: raw, MessageType: "invalidate",
	}); err != nil {
		t.Fatalf("Subscribe (first): %v", err)
	}

	wantNextFire := registerTime.Add(5 * time.Minute)
	if w := s1.watches["cron-1"]; w == nil {
		t.Fatal("first service: subscription not registered in-memory")
	} else if !w.NextFireAt.Equal(wantNextFire) {
		t.Fatalf("first service NextFireAt: got %s want %s", w.NextFireAt, wantNextFire)
	}

	persisted, err := state1.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll (first): %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted subscriptions: got %d want 1", len(persisted))
	}
	if !persisted[0].NextFireAt.Equal(wantNextFire) {
		t.Fatalf("persisted NextFireAt: got %s want %s", persisted[0].NextFireAt, wantNextFire)
	}

	if err := state1.Close(); err != nil {
		t.Fatalf("Close (first): %v", err)
	}
	s1 = nil

	state2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB (second): %v", err)
	}
	if state2 == nil {
		t.Fatal("openStateDB returned nil on restart with DSN set")
	}
	defer state2.Close()
	s2 := NewSensorService(srv.URL, noopLogger{})
	restartTime := registerTime.Add(6 * time.Minute)
	s2.clock = func() time.Time { return restartTime }
	s2.AttachStateDB(state2)

	rebuilt := s2.watches["cron-1"]
	if rebuilt == nil {
		t.Fatal("restart: subscription not rebuilt from state DB")
	}
	if !rebuilt.NextFireAt.Equal(wantNextFire) {
		t.Fatalf("restart NextFireAt: got %s want %s (recovered watermark, not recomputed)",
			rebuilt.NextFireAt, wantNextFire)
	}

	s2.Tick(ctx)

	if got := atomic.LoadInt64(fireCount); got != 1 {
		t.Fatalf("fireCount after restart Tick: got %d want 1 (recovered window must fire exactly once)", got)
	}
	bodiesMu.Lock()
	defer bodiesMu.Unlock()
	if len(*bodies) != 1 {
		t.Fatalf("recorded envelopes: got %d want 1", len(*bodies))
	}
	if sub, _ := (*bodies)[0]["publisher_subscription_id"].(string); sub == "" {
		t.Errorf("publisher_subscription_id: missing or empty (auth path discriminator)")
	}
	payload, _ := (*bodies)[0]["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("envelope payload missing/wrong shape: %+v", (*bodies)[0])
	}
	if fireAt, _ := payload["fire_at"].(string); fireAt != wantNextFire.UTC().Format(time.RFC3339) {
		t.Errorf("envelope fire_at: got %q want %q (must fire on the originally-scheduled window)",
			fireAt, wantNextFire.UTC().Format(time.RFC3339))
	}
}

func TestSubscribe_PersistedWatermarkPreservedWhenInMemoryWatchAbsent(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_CRON_STATE_DSN", dsn)

	srv, _, _, _ := newRecordingReceiver()
	defer srv.Close()

	state, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer state.Close()

	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)

	seed := NewSensorService(srv.URL, noopLogger{})
	seed.clock = func() time.Time { return registerTime }
	seed.state = state
	if _, err := seed.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "cron-1", InstanceId: "inst-1", Kind: "cron",
		ResolvedConfig: raw, MessageType: "invalidate",
	}); err != nil {
		t.Fatalf("seed Subscribe: %v", err)
	}
	wantNextFire := registerTime.Add(5 * time.Minute)

	late := NewSensorService(srv.URL, noopLogger{})
	late.clock = func() time.Time { return registerTime.Add(20 * time.Minute) }
	late.state = state
	if _, err := late.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "cron-1", InstanceId: "inst-1", Kind: "cron",
		ResolvedConfig: raw, MessageType: "invalidate",
	}); err != nil {
		t.Fatalf("late Subscribe: %v", err)
	}

	got := late.watches["cron-1"]
	if got == nil {
		t.Fatal("Subscribe did not register the watch in-memory")
	}
	if !got.NextFireAt.Equal(wantNextFire) {
		t.Fatalf("NextFireAt: got %s, want %s (persisted watermark must win over a wall-clock recompute "+
			"when the in-memory watch is absent but a DB row already exists)", got.NextFireAt, wantNextFire)
	}
}

func TestSubscribe_StateUpsertFailure_FailsRPCAndRollsBackInMemoryWatch(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_CRON_STATE_DSN", dsn)

	state, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	srv, _, _, _ := newRecordingReceiver()
	defer srv.Close()

	s := NewSensorService(srv.URL, noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s.state = state
	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	_, err = s.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "cron-1", InstanceId: "inst-1", Kind: "cron",
		ResolvedConfig: raw, MessageType: "invalidate",
	})
	if err == nil {
		t.Fatal("expected Subscribe to fail when the state DB upsert fails, " +
			"so the caller (publisher-lifecycle) retries instead of believing the subscription durable")
	}
	if _, exists := s.watches["cron-1"]; exists {
		t.Error("a failed persist must not leave an in-memory watch behind")
	}
}

func TestSensorCronStateDSN_UnsetLosesSubscriptionOnRestart(t *testing.T) {
	ctx := context.Background()
	t.Setenv("RIMSKY_SENSOR_CRON_STATE_DSN", "")

	srv, _, _, _ := newRecordingReceiver()
	defer srv.Close()

	state, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB (unset DSN): %v", err)
	}
	if state != nil {
		t.Fatal("openStateDB returned non-nil with DSN unset — in-memory default broken")
	}

	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s1 := NewSensorService(srv.URL, noopLogger{})
	s1.clock = func() time.Time { return registerTime }
	s1.AttachStateDB(state)

	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s1.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "cron-1", InstanceId: "inst-1", Kind: "cron",
		ResolvedConfig: raw, MessageType: "invalidate",
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	state2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB (restart, unset DSN): %v", err)
	}
	s2 := NewSensorService(srv.URL, noopLogger{})
	s2.clock = func() time.Time { return registerTime }
	s2.AttachStateDB(state2)

	resp, err := s2.ListSubscriptions(ctx, nil)
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(resp.GetSubscriptions()) != 0 {
		t.Fatalf("in-memory restart: got %d subscriptions, want 0 (state lost without DSN)",
			len(resp.GetSubscriptions()))
	}
}
