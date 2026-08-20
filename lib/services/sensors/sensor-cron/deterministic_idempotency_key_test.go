// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor

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

const subscriptionWindowCron = "*/5 * * * *"

func subscribeCron(t *testing.T, s *SensorService, subscriptionID string, at time.Time) {
	t.Helper()
	s.clock = func() time.Time { return at }
	raw, err := json.Marshal(map[string]any{"cron": subscriptionWindowCron})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: subscriptionID, InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOneFireWindowPostsOneEnvelopeNamingItsSubscription(t *testing.T) {
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
	subscribeCron(t, s, "w1", registerTime)

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

func TestRefiringTheSameWindowAfterARestartIsAbsorbedByTheIdempotentSend(t *testing.T) {
	srv, recv := newDedupingReceiver()
	defer srv.Close()

	beforeRestart := NewSensorService(srv.URL, noopLogger{})
	afterRestart := NewSensorService(srv.URL, noopLogger{})

	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fireWindow := registerTime.Add(6 * time.Minute)
	for _, s := range []*SensorService{beforeRestart, afterRestart} {
		subscribeCron(t, s, "w1", registerTime)
		s.clock = func() time.Time { return fireWindow }
		s.Tick(context.Background())
	}

	total, accepted := recv.snapshot()
	if total != 2 {
		t.Errorf("POSTs after the restart refired the window: got %d, want 2 "+
			"(a restarted sensor carries no memory of the fire and posts again)", total)
	}
	if accepted != 1 {
		t.Errorf("dedup-accepted messages: got %d, want 1 "+
			"(the same subscription and the same fire window derive the same idempotency "+
			"key, so the control API's idempotent send absorbs the second post)", accepted)
	}
}

func TestDistinctSubscriptionsNeverShareAnIdempotencyKey(t *testing.T) {
	srv, recv := newDedupingReceiver()
	defer srv.Close()

	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fireWindow := registerTime.Add(6 * time.Minute)
	for _, subscriptionID := range []string{"w1", "w2"} {
		s := NewSensorService(srv.URL, noopLogger{})
		subscribeCron(t, s, subscriptionID, registerTime)
		s.clock = func() time.Time { return fireWindow }
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
