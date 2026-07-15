// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestSubscribe_RestartReplay_PreloadsLastIdempotency(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s1.Close()
	w := &Watch{
		SubscriptionID:    "sub-2",
		InstanceID:        "inst-2",
		PathPrefix:        "/wh/restart",
		IdempotencyHeader: "X-Idem",

		MessageType: "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateLastIdempotency(ctx, "sub-2", "post-restart-key"); err != nil {
		t.Fatalf("UpdateLastIdempotency: %v", err)
	}

	got, err := s1.GetSubscription(ctx, "sub-2")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got == nil {
		t.Fatal("GetSubscription returned nil for known subscription_id")
	}
	if got.LastIdempotencyKey != "post-restart-key" {
		t.Fatalf("expected LastIdempotencyKey=post-restart-key, got %q", got.LastIdempotencyKey)
	}

	got, err = s1.GetSubscription(ctx, "sub-nonexistent")
	if err != nil {
		t.Fatalf("GetSubscription nonexistent: %v", err)
	}
	if got != nil {
		t.Fatal("GetSubscription should return nil for unknown id")
	}
}

func TestAttachStateDB_RestartRestoresPathBindings(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	persisted := &Watch{
		SubscriptionID:    "sub-3",
		InstanceID:        "inst-3",
		PathPrefix:        "/wh/restored",
		IdempotencyHeader: "X-Idem",
		MessageType:       "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, persisted); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateLastIdempotency(ctx, "sub-3", "seen-key"); err != nil {
		t.Fatalf("UpdateLastIdempotency: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var (
		mu     sync.Mutex
		pushed int
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		pushed++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	svc := NewSensorService(rimsky.URL, router, noopLogger{})
	s2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB after restart: %v", err)
	}
	defer s2.Close()
	svc.AttachStateDB(s2)

	srv := httptest.NewServer(router)
	defer srv.Close()

	post := func(key string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/restored", bytes.NewReader([]byte(`{"event":"x"}`)))
		req.Header.Set("X-Idem", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := post("seen-key"); code != http.StatusOK {
		t.Errorf("replayed key after restart: %d (want 200 dedup, 404 means binding lost)", code)
	}
	mu.Lock()
	if pushed != 0 {
		t.Errorf("pushed for replayed key: %d (want 0)", pushed)
	}
	mu.Unlock()

	if code := post("fresh-key"); code != http.StatusOK {
		t.Errorf("fresh key after restart: %d (want 200)", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if pushed != 1 {
		t.Errorf("pushed for fresh key: %d (want 1)", pushed)
	}
}

func TestStateDB_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	if s1 == nil {
		t.Fatal("openStateDB returned nil with DSN set")
	}
	w := &Watch{
		SubscriptionID:    "sub-1",
		InstanceID:        "inst-1",
		PathPrefix:        "/wh/abc",
		IdempotencyHeader: "X-Idem",

		MessageType: "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateLastIdempotency(ctx, "sub-1", "delivery-key-42"); err != nil {
		t.Fatalf("UpdateLastIdempotency: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB after restart: %v", err)
	}
	defer s2.Close()
	subs, err := s2.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0].SubscriptionID != "sub-1" || subs[0].LastIdempotencyKey != "delivery-key-42" {
		t.Errorf("subscription state did not roundtrip: %+v", subs[0])
	}
}
