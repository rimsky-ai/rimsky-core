// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
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
