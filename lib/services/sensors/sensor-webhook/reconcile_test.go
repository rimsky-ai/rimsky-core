// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/sensorauth"
)

func postWebhook(t *testing.T, base, path, idemHeader, idemKey string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader([]byte(`{"event":"x"}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if idemHeader != "" {
		req.Header.Set(idemHeader, idemKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestAttachStateDB_RestoresWatermarkCacheBeforeAnySubscribe(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN", dsn)

	const subID = "sub-attach"
	seed, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB (seed): %v", err)
	}
	if err := seed.UpdateLastIdempotency(ctx, subID, "seeded-key"); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	router := chi.NewRouter()
	svc := NewSensorService("", router, noopLogger{})
	state, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer state.Close()

	svc.AttachStateDB(state)

	svc.mu.Lock()
	cached, ok := svc.watermarkCache[subID]
	svc.mu.Unlock()
	if !ok {
		t.Fatalf("watermarkCache missing %q immediately after AttachStateDB", subID)
	}
	if cached != "seeded-key" {
		t.Fatalf("watermarkCache[%q] = %q, want %q", subID, cached, "seeded-key")
	}

	subscribeWithAuth(t, svc, subID, "/wh/attach", map[string]any{"mode": sensorauth.ModeNone})

	svc.mu.Lock()
	restored := svc.watches[subID]
	_, stillCached := svc.watermarkCache[subID]
	svc.mu.Unlock()
	if restored == nil {
		t.Fatalf("watch %q not registered after subscribe", subID)
	}
	if restored.LastIdempotency != "seeded-key" {
		t.Fatalf("watch.LastIdempotency = %q, want %q", restored.LastIdempotency, "seeded-key")
	}
	if stillCached {
		t.Fatalf("watermarkCache[%q] not cleared after subscribe consumed it", subID)
	}
}

func TestReconcile_RestartRehydratesWatermarkAndBindingsLiveAgain(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_WEBHOOK_STATE_DSN", dsn)

	const subID = "sub-restart"
	const path = "/wh/restart"
	const idemHeader = "X-Idem"
	auth := map[string]any{"mode": sensorauth.ModeNone}

	var pushedBefore int32
	rimskyBefore := countingRimsky(&pushedBefore)
	defer rimskyBefore.Close()

	router1 := chi.NewRouter()
	svc1 := NewSensorService(rimskyBefore.URL, router1, noopLogger{})
	state1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	svc1.AttachStateDB(state1)
	subscribeWebhook(t, svc1, subID, path, idemHeader, auth)

	srv1 := httptest.NewServer(router1)
	if code := postWebhook(t, srv1.URL, path, idemHeader, "k1"); code != http.StatusOK {
		t.Fatalf("pre-restart delivery status = %d, want 200", code)
	}
	if got := atomic.LoadInt32(&pushedBefore); got != 1 {
		t.Fatalf("pre-restart pushed = %d, want 1", got)
	}
	srv1.Close()
	if err := state1.Close(); err != nil {
		t.Fatalf("close state1: %v", err)
	}

	var pushedAfter int32
	rimskyAfter := countingRimsky(&pushedAfter)
	defer rimskyAfter.Close()

	router2 := chi.NewRouter()
	svc2 := NewSensorService(rimskyAfter.URL, router2, noopLogger{})
	state2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB after restart: %v", err)
	}
	defer state2.Close()
	svc2.AttachStateDB(state2)

	srv2 := httptest.NewServer(router2)
	defer srv2.Close()

	if code := postWebhook(t, srv2.URL, path, idemHeader, "k2"); code != http.StatusNotFound {
		t.Fatalf("post before reconcile tick status = %d, want 404 (no route bound yet)", code)
	}

	subscribeWebhook(t, svc2, subID, path, idemHeader, auth)

	if code := postWebhook(t, srv2.URL, path, idemHeader, "k1"); code != http.StatusOK {
		t.Fatalf("replayed key after reconcile status = %d, want 200 dedup", code)
	}
	if got := atomic.LoadInt32(&pushedAfter); got != 0 {
		t.Fatalf("pushedAfter = %d after replayed key, want 0 (watermark must have rehydrated)", got)
	}

	if code := postWebhook(t, srv2.URL, path, idemHeader, "k2"); code != http.StatusOK {
		t.Fatalf("fresh key after reconcile status = %d, want 200", code)
	}
	if got := atomic.LoadInt32(&pushedAfter); got != 1 {
		t.Fatalf("pushedAfter = %d after fresh key, want 1 (binding must be live again)", got)
	}
}

func TestReconcile_ExplicitTicksConvergeIdempotently(t *testing.T) {
	const subID = "sub-tick"
	const path = "/wh/tick"

	var pushed int32
	rimsky := countingRimsky(&pushed)
	defer rimsky.Close()

	router := chi.NewRouter()
	svc := NewSensorService(rimsky.URL, router, noopLogger{})
	srv := httptest.NewServer(router)
	defer srv.Close()

	if code := postWebhook(t, srv.URL, path, "", ""); code != http.StatusNotFound {
		t.Fatalf("post before first tick status = %d, want 404", code)
	}

	tick := func() {
		t.Helper()
		subscribeWithAuth(t, svc, subID, path, map[string]any{"mode": sensorauth.ModeNone})
	}

	tick()
	if code := postWebhook(t, srv.URL, path, "", ""); code != http.StatusOK {
		t.Fatalf("post after tick 1 status = %d, want 200", code)
	}
	if got := atomic.LoadInt32(&pushed); got != 1 {
		t.Fatalf("pushed after tick 1 = %d, want 1", got)
	}

	svc.mu.Lock()
	boundAfterTick1 := svc.pathToWatch[path]
	svc.mu.Unlock()
	if boundAfterTick1 == nil {
		t.Fatalf("path %q not bound after tick 1", path)
	}

	for i := 2; i <= 4; i++ {
		tick()

		svc.mu.Lock()
		watchCount := len(svc.watches)
		pathCount := len(svc.pathToWatch)
		bound := svc.pathToWatch[path]
		svc.mu.Unlock()

		if watchCount != 1 || pathCount != 1 {
			t.Fatalf("after tick %d: watches=%d pathToWatch=%d, want 1 and 1 (no duplicate registration)", i, watchCount, pathCount)
		}
		if bound != boundAfterTick1 {
			t.Fatalf("after tick %d: path rebound to a different watch instance", i)
		}
	}

	if code := postWebhook(t, srv.URL, path, "", ""); code != http.StatusOK {
		t.Fatalf("post after repeated ticks status = %d, want 200", code)
	}
	if got := atomic.LoadInt32(&pushed); got != 2 {
		t.Fatalf("pushed after repeated ticks = %d, want 2 (still delivering, not duplicated)", got)
	}
}
