// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

type dedupingReceiver struct {
	mu            sync.Mutex
	accepted      map[string]bool
	acceptedCount int
	totalPosts    int
}

func newDedupingReceiver() (*httptest.Server, *dedupingReceiver) {
	d := &dedupingReceiver{accepted: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		d.mu.Lock()
		d.totalPosts++
		if !d.accepted[key] {
			d.accepted[key] = true
			d.acceptedCount++
		}
		d.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	return srv, d
}

func (d *dedupingReceiver) snapshot() (total, accepted int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.totalPosts, d.acceptedCount
}

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
		MessageType: "invalidate",
	})
	if err != nil {
		t.Fatal(err)
	}

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
	if sub, _ := bodies[0]["publisher_subscription_id"].(string); sub == "" {
		t.Errorf("publisher_subscription_id: missing or empty (auth path discriminator)")
	}
}

func TestMultiReplica_SameSubscriptionSameWindow_CollapsesToOneAcceptedMessage(t *testing.T) {
	srv, recv := newDedupingReceiver()
	defer srv.Close()

	replicaA := NewSensorService(srv.URL, noopLogger{})
	replicaB := NewSensorService(srv.URL, noopLogger{})

	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, s := range []*SensorService{replicaA, replicaB} {
		s.clock = func() time.Time { return registerTime }
		cfg := map[string]any{"cron": "*/5 * * * *"}
		raw, _ := json.Marshal(cfg)
		_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
			PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
			MessageType: "invalidate",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, s := range []*SensorService{replicaA, replicaB} {
		s.clock = func() time.Time { return registerTime.Add(6 * time.Minute) }
		s.Tick(context.Background())
	}

	total, accepted := recv.snapshot()
	if total != 2 {
		t.Errorf("POSTs from two independent replicas: got %d, want 2 "+
			"(no cross-replica coordination; each replica fires on its own)", total)
	}
	if accepted != 1 {
		t.Errorf("dedup-accepted messages: got %d, want 1 "+
			"(same subscription + same fire window must produce identical idempotency "+
			"keys, so a real control-API dedup collapses the two POSTs to one enqueued "+
			"message; this is deliberate, not leader election)", accepted)
	}
}

func TestMultiReplica_DistinctSubscriptions_NeverCollapse(t *testing.T) {
	srv, recv := newDedupingReceiver()
	defer srv.Close()

	replicaA := NewSensorService(srv.URL, noopLogger{})
	replicaB := NewSensorService(srv.URL, noopLogger{})

	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := map[string]any{"cron": "*/5 * * * *"}
	raw, _ := json.Marshal(cfg)
	subs := map[*SensorService]string{replicaA: "w1", replicaB: "w2"}
	for s, subID := range subs {
		s.clock = func() time.Time { return registerTime }
		_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
			PublisherSubscriptionId: subID, InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
			MessageType: "invalidate",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for s := range subs {
		s.clock = func() time.Time { return registerTime.Add(6 * time.Minute) }
		s.Tick(context.Background())
	}

	total, accepted := recv.snapshot()
	if total != 2 {
		t.Errorf("POSTs: got %d, want 2", total)
	}
	if accepted != 2 {
		t.Errorf("dedup-accepted messages: got %d, want 2 "+
			"(distinct subscriptions must never collapse into each other)", accepted)
	}
}
