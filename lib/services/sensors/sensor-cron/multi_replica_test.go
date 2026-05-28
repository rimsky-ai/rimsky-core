// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// multi_replica_test.go pins single-replica behavior. Per
// `concept:replica`, sensor-cron's v1 contract is single-replica only:
// operators run one pod per binary and accept restart-on-fail
// recovery. Multi-replica HA is the publisher implementation's concern,
// not rimsky's. A sensor-cron binary deployed as multiple replicas
// behind a single rimsky endpoint will double-fire every tick — this
// is honest behavior, not a bug to coordinate around.
//
// The tests below pin both shapes:
//
//  1. Single replica fires once per window.
//  2. Two independently-running in-process replicas (each with its own
//     SensorService instance) fire INDEPENDENTLY — no cross-replica
//     serialization exists by design.
//
// @concept: sensor
// @concept: replica

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
)

// TestSingleReplica_FiresOnceWhenSubscriptionTickFires pins the
// standard single-replica behavior. A publisher-subscription with a
// cron expression that's already due fires exactly once on the first
// Tick call.
func TestSingleReplica_FiresOnceWhenSubscriptionTickFires(t *testing.T) {
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
	defer srv.Close()

	s := NewSensorService(srv.URL, noopLogger{})
	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return registerTime }
	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
		TargetNode: "tick", MessageKind: "invalidate",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Advance the clock past the next-fire time.
	s.clock = func() time.Time { return registerTime.Add(6 * time.Minute) }
	s.Tick(context.Background())

	if got := atomic.LoadInt64(&fireCount); got != 1 {
		t.Errorf("fireCount: got %d want 1", got)
	}
	bodiesMu.Lock()
	defer bodiesMu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("bodies: got %d want 1", len(bodies))
	}
	if bodies[0]["sender_kind"] != "publisher" {
		t.Errorf("sender_kind: got %v", bodies[0]["sender_kind"])
	}
}

// TestMultiReplica_TwoInProcessInstancesEachFireIndependently pins the
// v1 contract: two replicas of sensor-cron pointed at the same rimsky
// endpoint each fire independently. Per `concept:replica`, rimsky
// does not model replica coordination — operators run a single replica
// per publisher binary. This test documents the honest behavior so
// operators reading the test suite see exactly what happens at
// replicas > 1.
func TestMultiReplica_TwoInProcessInstancesEachFireIndependently(t *testing.T) {
	var fireCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&fireCount, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	// Two independent SensorService instances; same
	// publisher_subscription_id. Each replica is shape-isolated; no
	// shared state.
	replicaA := NewSensorService(srv.URL, noopLogger{})
	replicaB := NewSensorService(srv.URL, noopLogger{})

	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, s := range []*SensorService{replicaA, replicaB} {
		s.clock = func() time.Time { return registerTime }
		cfg := map[string]any{"cron": "*/5 * * * *"}
		raw, _ := json.Marshal(cfg)
		_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
			PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
			TargetNode: "tick", MessageKind: "invalidate",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Advance both clocks; both ticks should fire — single-replica is
	// the v1 contract per `concept:replica`.
	for _, s := range []*SensorService{replicaA, replicaB} {
		s.clock = func() time.Time { return registerTime.Add(6 * time.Minute) }
		s.Tick(context.Background())
	}

	if got := atomic.LoadInt64(&fireCount); got != 2 {
		t.Errorf("fireCount: got %d, want 2 (v1 contract is single-replica per concept:replica). At replicas=N the fan-out is N×.", got)
	}
}
