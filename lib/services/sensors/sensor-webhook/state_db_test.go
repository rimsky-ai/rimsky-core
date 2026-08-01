// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func stateColumns(ctx context.Context, t *testing.T, s *stateDB) []string {
	t.Helper()
	rows, err := s.db.QueryContext(ctx,
		`SELECT column_name FROM information_schema.columns WHERE table_name = 'sensor_webhook_state'`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols
}

func dumpStateRows(ctx context.Context, t *testing.T, s *stateDB) string {
	t.Helper()
	rows, err := s.db.QueryContext(ctx, `SELECT to_jsonb(t)::text FROM sensor_webhook_state t`)
	if err != nil {
		t.Fatalf("dump rows: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		b.WriteString(j)
		b.WriteString("\n")
	}
	return b.String()
}

func subscribeWebhook(t *testing.T, s *SensorService, subID, path, idemHeader string, auth map[string]any) {
	t.Helper()
	cfg := map[string]any{"path_prefix": path, "idempotency_header": idemHeader, "auth": auth}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: subID, InstanceId: "i1", Kind: "webhook", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatalf("subscribe %s: %v", subID, err)
	}
}

func TestStateDB_OnlyPersistsWatermarkNeverConfig(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN", dsn)

	s, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s.Close()

	got := stateColumns(ctx, t, s)
	want := []string{"last_idempotency_key", "last_seen_at", "publisher_subscription_id"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("state schema columns = %v, want exactly %v (config/secret columns must be gone)", got, want)
	}
}

func TestStateDB_NeverPersistsSecret(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN", dsn)

	var pushed int32
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&pushed, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	svc := NewSensorService(rimsky.URL, router, noopLogger{})
	state, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer state.Close()
	svc.AttachStateDB(state)

	const secret = "super-secret-shared-value"
	subscribeWebhook(t, svc, "sub-secret", "/wh/secret", "X-Idem", map[string]any{
		"mode": authModeSecretHeader, "header": "X-Token", "secret": secret,
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/secret", bytes.NewReader([]byte(`{"event":"x"}`)))
	req.Header.Set("X-Token", secret)
	req.Header.Set("X-Idem", "delivery-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post webhook: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook post status %d (want 200)", resp.StatusCode)
	}
	if atomic.LoadInt32(&pushed) != 1 {
		t.Fatalf("pushed = %d, want 1", atomic.LoadInt32(&pushed))
	}

	dump := dumpStateRows(ctx, t, state)
	if strings.Contains(dump, secret) {
		t.Fatalf("secret leaked into state db: %s", dump)
	}
	if cols := stateColumns(ctx, t, state); strings.Join(cols, ",") != "last_idempotency_key,last_seen_at,publisher_subscription_id" {
		t.Fatalf("config columns still present: %v", cols)
	}
	key, err := state.GetLastIdempotency(ctx, "sub-secret")
	if err != nil {
		t.Fatalf("GetLastIdempotency: %v", err)
	}
	if key != "delivery-1" {
		t.Fatalf("watermark = %q, want delivery-1", key)
	}
}

func TestSubscribe_ResyncReloadsWatermarkAfterRestart(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN", dsn)

	auth := map[string]any{"mode": authModeNone}

	var pushed1 int32
	rimsky1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&pushed1, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky1.Close()

	router1 := chi.NewRouter()
	svc1 := NewSensorService(rimsky1.URL, router1, noopLogger{})
	state1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	svc1.AttachStateDB(state1)
	subscribeWebhook(t, svc1, "sub-resync", "/wh/resync", "X-Idem", auth)

	srv1 := httptest.NewServer(router1)
	post := func(base, key string) int {
		req, _ := http.NewRequest(http.MethodPost, base+"/wh/resync", bytes.NewReader([]byte(`{"event":"x"}`)))
		req.Header.Set("X-Idem", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(srv1.URL, "k1"); code != http.StatusOK {
		t.Fatalf("first delivery status %d (want 200)", code)
	}
	if atomic.LoadInt32(&pushed1) != 1 {
		t.Fatalf("pushed1 = %d, want 1", atomic.LoadInt32(&pushed1))
	}
	srv1.Close()
	if err := state1.Close(); err != nil {
		t.Fatalf("close state1: %v", err)
	}

	var pushed2 int32
	rimsky2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&pushed2, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky2.Close()

	router2 := chi.NewRouter()
	svc2 := NewSensorService(rimsky2.URL, router2, noopLogger{})
	state2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB after restart: %v", err)
	}
	defer state2.Close()
	svc2.AttachStateDB(state2)

	svc2.mu.Lock()
	restoredWatches := len(svc2.watches)
	svc2.mu.Unlock()
	if restoredWatches != 0 {
		t.Fatalf("restart restored %d watches from its own db; config must come from resync", restoredWatches)
	}

	subscribeWebhook(t, svc2, "sub-resync", "/wh/resync", "X-Idem", auth)

	srv2 := httptest.NewServer(router2)
	defer srv2.Close()

	if code := post(srv2.URL, "k1"); code != http.StatusOK {
		t.Fatalf("replayed key after resync status %d (want 200 dedup)", code)
	}
	if atomic.LoadInt32(&pushed2) != 0 {
		t.Fatalf("pushed2 = %d after replayed key, want 0 (watermark did not resume dedup)", atomic.LoadInt32(&pushed2))
	}
	if code := post(srv2.URL, "k2"); code != http.StatusOK {
		t.Fatalf("fresh key after resync status %d (want 200)", code)
	}
	if atomic.LoadInt32(&pushed2) != 1 {
		t.Fatalf("pushed2 = %d after fresh key, want 1", atomic.LoadInt32(&pushed2))
	}
}
