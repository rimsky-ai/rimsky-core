// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func seedEventAt(t *testing.T, ctx context.Context, store persistence.Tables, k events.Kind, occurredAt time.Time) {
	t.Helper()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Events().Append(ctx, persistence.EventAppendInput{
			Kind:       k,
			Payload:    map[string]any{},
			OccurredAt: &occurredAt,
		}, tx)
	}); err != nil {
		t.Fatalf("seedEventAt: %v", err)
	}
}

func TestHandler_ListEvents_KindInFilter(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	now := time.Now().UTC()

	seedEventAt(t, ctx, store, events.KindWorkStarted(), now)
	seedEventAt(t, ctx, store, events.SignalKind("terminal/success"), now.Add(time.Second))
	seedEventAt(t, ctx, store, events.SignalKind("terminal/failure"), now.Add(2*time.Second))

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/events?kind_in=terminal/success,terminal/failure", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Events) != 2 {
		t.Fatalf("kind_in filter returned %d events, want 2: %+v", len(body.Events), body.Events)
	}
	kinds := map[string]bool{}
	for _, e := range body.Events {
		kinds[e["kind"].(string)] = true
	}
	if !kinds["terminal/success"] || !kinds["terminal/failure"] {
		t.Fatalf("kind_in filter did not return the expected kinds: %+v", kinds)
	}
	if kinds["work_started"] {
		t.Fatalf("kind_in filter leaked an event outside the requested kinds: %+v", kinds)
	}
}

func TestHandler_ListEvents_SinceFilter(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	base := time.Now().UTC()

	seedEventAt(t, ctx, store, events.SignalKind("terminal/old"), base)
	cutoff := base.Add(time.Minute)
	seedEventAt(t, ctx, store, events.SignalKind("terminal/new"), base.Add(2*time.Minute))

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/events?since="+cutoff.Format(time.RFC3339Nano), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Events) != 1 {
		t.Fatalf("since filter returned %d events, want 1: %+v", len(body.Events), body.Events)
	}
	if body.Events[0]["kind"] != "terminal/new" {
		t.Fatalf("since filter returned %v, want terminal/new", body.Events[0]["kind"])
	}
}

func TestHandler_ListEvents_SinceFilterInvalidTimestamp(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/events?since=not-a-timestamp", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
