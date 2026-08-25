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

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/sensorauth"
)

func stateColumns(ctx context.Context, t *testing.T, s *stateDB) []string {
	t.Helper()
	rows, err := s.db.Query(ctx,
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
	rows, err := s.db.Query(ctx, `SELECT to_jsonb(t)::text FROM sensor_webhook_state t`)
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
		"mode": sensorauth.ModeSecretHeader, "header": "X-Token", "secret": secret,
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
