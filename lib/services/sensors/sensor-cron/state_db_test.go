// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state_db_test.go — pgtest-backed coverage for sensor-cron's durable
// state persistence (S-sensors-cron-state-dsn-durability). Confirms that
// active cron publisher-subscriptions and their next-fire watermarks
// survive a process restart when RIMSKY_SENSOR_CRON_STATE_DSN points at a
// real Postgres, and that an empty/unset DSN keeps today's in-memory
// default (subscription lost on restart, recovered only via Subscribe
// replay).
//
// Modeled on the sensor-http peer (sensor-http/state_db_test.go) but the
// load-bearing observable is firing on the *originally-scheduled* window
// after a restart: the recovered watermark must be the persisted
// next_fire_at, NOT a freshly-recomputed sched.Next(now). That distinction
// is what proves durability rather than mere reconstruction — so the
// assertions deliberately pin the persisted next_fire_at AND the
// fire-on-the-original-window behavior, not just "a subscription exists".
//
// This drives the real Publisher service end to end against a real
// Postgres (testcontainers). It does not stub the state layer.

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

// newRecordingReceiver stands up an httptest.Server that records every POSTed
// message envelope. Mirrors multi_replica_test.go#47-57.
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

// TestSensorCronStateDSN_SurvivesRestartAndFiresOnScheduledWindow drives
// the durability contract of S-sensors-cron-state-dsn-durability: a cron
// publisher-subscription registered with a FUTURE next_fire_at persists to
// the state DB, survives a simulated process death (drop the service, do
// NOT Unsubscribe), and on restart is rebuilt from durable storage with its
// ORIGINAL next_fire_at — so the second service fires on the
// originally-scheduled window without any externally-driven re-Subscribe.
//
// RED until SENSLIFEOBS-2 ships openStateDB / AttachStateDB / the
// RIMSKY_SENSOR_CRON_STATE_DSN wiring and the cron-specific state schema.
func TestSensorCronStateDSN_SurvivesRestartAndFiresOnScheduledWindow(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_CRON_STATE_DSN", dsn)

	srv, fireCount, bodies, bodiesMu := newRecordingReceiver()
	defer srv.Close()

	// @deliberate: registerTime fixed at 00:00:00 so the */5 schedule's next
	// fire (00:05:00) is strictly FUTURE — nothing fires at registration and
	// the persisted watermark is the load-bearing state under test.
	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// @constraint: first service registers + persists + dies (without Unsubscribe).
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
		ResolvedConfig: raw, TargetNode: "tick", MessageType: "invalidate",
	}); err != nil {
		t.Fatalf("Subscribe (first): %v", err)
	}

	// @constraint: wantNextFire is the cron's first fire after registerTime —
	// the watermark the first service must persist.
	wantNextFire := registerTime.Add(5 * time.Minute)
	if w := s1.watches["cron-1"]; w == nil {
		t.Fatal("first service: subscription not registered in-memory")
	} else if !w.NextFireAt.Equal(wantNextFire) {
		t.Fatalf("first service NextFireAt: got %s want %s", w.NextFireAt, wantNextFire)
	}

	// @constraint: round-trip must be confirmed BEFORE the restart, so the
	// post-restart recovery is genuinely reading from Postgres (not from a
	// race-survived in-memory map).
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

	// @deliberate: simulate process death by closing the DB handle WITHOUT
	// calling Unsubscribe — the row must remain in Postgres for recovery.
	if err := state1.Close(); err != nil {
		t.Fatalf("Close (first): %v", err)
	}
	s1 = nil

	// @constraint: second service is a fresh process pointed at the SAME DSN.
	state2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB (second): %v", err)
	}
	if state2 == nil {
		t.Fatal("openStateDB returned nil on restart with DSN set")
	}
	defer state2.Close()
	s2 := NewSensorService(srv.URL, noopLogger{})
	// @deliberate: pin the second clock LATER than registerTime so we can
	// prove the recovered watermark is the PERSISTED next_fire_at (00:05:00)
	// and not a fresh sched.Next(restartTime). If recovery recomputed from
	// this clock, the next fire would be 00:10:00 (the first */5 boundary
	// strictly after 00:06:00) and the 00:05:00 fire below would be missed.
	restartTime := registerTime.Add(6 * time.Minute)
	s2.clock = func() time.Time { return restartTime }
	// @constraint: AttachStateDB must rebuild s.watches from state.ListAll.
	s2.AttachStateDB(state2)

	// @constraint: rebuilt watch must carry the ORIGINAL persisted
	// next_fire_at (00:05:00), not a recomputed one (00:10:00).
	rebuilt := s2.watches["cron-1"]
	if rebuilt == nil {
		t.Fatal("restart: subscription not rebuilt from state DB")
	}
	if !rebuilt.NextFireAt.Equal(wantNextFire) {
		t.Fatalf("restart NextFireAt: got %s want %s (recovered watermark, not recomputed)",
			rebuilt.NextFireAt, wantNextFire)
	}

	// @constraint: load-bearing proof — restartTime (00:06:00) is already past
	// the persisted next_fire_at (00:05:00), so a single Tick must fire
	// exactly once, on the original window, with no re-Subscribe.
	s2.Tick(ctx)

	if got := atomic.LoadInt64(fireCount); got != 1 {
		t.Fatalf("fireCount after restart Tick: got %d want 1 (recovered window must fire exactly once)", got)
	}
	bodiesMu.Lock()
	defer bodiesMu.Unlock()
	if len(*bodies) != 1 {
		t.Fatalf("recorded envelopes: got %d want 1", len(*bodies))
	}
	if (*bodies)[0]["sender_kind"] != "publisher" {
		t.Errorf("sender_kind: got %v want publisher", (*bodies)[0]["sender_kind"])
	}
	// @constraint: envelope fire_at must be the original window (00:05:00),
	// confirming the fire used the recovered watermark, not a fresh window.
	payload, _ := (*bodies)[0]["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("envelope payload missing/wrong shape: %+v", (*bodies)[0])
	}
	if fireAt, _ := payload["fire_at"].(string); fireAt != wantNextFire.UTC().Format(time.RFC3339) {
		t.Errorf("envelope fire_at: got %q want %q (must fire on the originally-scheduled window)",
			fireAt, wantNextFire.UTC().Format(time.RFC3339))
	}
}

// TestSensorCronStateDSN_UnsetLosesSubscriptionOnRestart pins the in-memory
// default: with RIMSKY_SENSOR_CRON_STATE_DSN UNSET, openStateDB returns nil,
// AttachStateDB(nil) is a no-op, and a restart (a fresh SensorService) holds
// no subscriptions — they are recovered only via Subscribe replay from
// rimsky's ResyncPublisherSubscriptions, exactly as before this feature.
//
// RED until openStateDB / AttachStateDB exist.
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
	s1.AttachStateDB(state) // @constraint: nil state → no-op

	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	if _, err := s1.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "cron-1", InstanceId: "inst-1", Kind: "cron",
		ResolvedConfig: raw, TargetNode: "tick", MessageType: "invalidate",
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// @deliberate: simulated restart — a fresh service with a fresh (nil) state DB.
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
