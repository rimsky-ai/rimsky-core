// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/fallguy/rimsky/control/observability"
	"github.com/fallguy/rimsky/foundation/persistence"
	_ "github.com/fallguy/rimsky/foundation/persistence/sqlite"
	"github.com/fallguy/rimsky/foundation/shared"
)

// newSQLiteDriver builds an in-memory-ish sqlite driver with migrations
// applied; ready to seed.
func newSQLiteDriver(t *testing.T) persistence.Database {
	t.Helper()
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "obs.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// newRouter wires the observability handlers under /v1/observability.
func newRouter(t *testing.T, deps observability.Deps) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/observability", func(rr chi.Router) {
		observability.Routes(rr, deps)
	})
	return r
}

func TestHandler_SystemSummary_EmptyDB(t *testing.T) {
	d := newSQLiteDriver(t)
	disc := observability.RunHandshake(context.Background(), &nopProber{},
		nil, nil, slog.Default())
	deps := observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Discovery: disc,
	}
	r := newRouter(t, deps)
	req := httptest.NewRequest("GET", "/v1/observability/system/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["instances_active"].(float64) != 0 {
		t.Fatalf("instances_active = %v, want 0", body["instances_active"])
	}
}

func TestHandler_ListStores_DeclaredOnly(t *testing.T) {
	d := newSQLiteDriver(t)
	disc := observability.RunHandshake(context.Background(), &nopProber{},
		nil,
		[]observability.PeerSpec{{Name: "topics-ring", Endpoint: "store:9000"}},
		slog.Default())
	deps := observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Stores:    []observability.PeerSpec{{Name: "topics-ring", Endpoint: "store:9000"}},
		Discovery: disc,
	}
	r := newRouter(t, deps)
	req := httptest.NewRequest("GET", "/v1/observability/stores", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Stores []map[string]any `json:"stores"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Stores) != 1 || body.Stores[0]["name"] != "topics-ring" {
		t.Fatalf("stores = %+v", body.Stores)
	}
}

func TestHandler_GetExecutor_NotFound(t *testing.T) {
	d := newSQLiteDriver(t)
	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Discovery: disc,
	}
	r := newRouter(t, deps)
	req := httptest.NewRequest("GET", "/v1/observability/executors/missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandler_ListEvents_BadFilter(t *testing.T) {
	d := newSQLiteDriver(t)
	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Discovery: disc,
	}
	r := newRouter(t, deps)
	req := httptest.NewRequest("GET", "/v1/observability/events?node_id=not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ListDispatches_Empty(t *testing.T) {
	d := newSQLiteDriver(t)
	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Discovery: disc,
	}
	r := newRouter(t, deps)
	req := httptest.NewRequest("GET", "/v1/observability/node-runs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		NodeRuns   []map[string]any `json:"node_runs"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.NodeRuns) != 0 {
		t.Fatalf("node_runs = %+v, want empty", body.NodeRuns)
	}
}

func TestHandler_ListFrames_Empty(t *testing.T) {
	d := newSQLiteDriver(t)
	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Discovery: disc,
	}
	r := newRouter(t, deps)
	req := httptest.NewRequest("GET", "/v1/observability/frames", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Frames []map[string]any `json:"frames"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Frames) != 0 {
		t.Fatalf("frames = %+v, want empty", body.Frames)
	}
}

func TestHandler_GetFrame_NotFound(t *testing.T) {
	d := newSQLiteDriver(t)
	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Discovery: disc,
	}
	r := newRouter(t, deps)
	req := httptest.NewRequest("GET", "/v1/observability/frames/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandler_SystemSummary_DispatchCounts(t *testing.T) {
	d := newSQLiteDriver(t)
	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Discovery: disc,
	}
	r := newRouter(t, deps)
	req := httptest.NewRequest("GET", "/v1/observability/system/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := body["node_runs_claimed"]; !ok {
		t.Fatalf("missing node_runs_claimed: body=%+v", body)
	}
	if _, ok := body["node_runs_pending"]; !ok {
		t.Fatalf("missing node_runs_pending: body=%+v", body)
	}
}

// nopProber returns an unreachable response for every probe — fits the
// handler tests that don't need a real peer.
type nopProber struct{}

func (*nopProber) ProbeExecutor(_ context.Context, _ string) (*observability.ObservabilityCapabilities, error) {
	return nil, errProbeUnreachable
}
func (*nopProber) ProbeStore(_ context.Context, _ string) (*observability.ObservabilityCapabilities, error) {
	return nil, errProbeUnreachable
}

var errProbeUnreachable = &probeError{msg: "unreachable"}

type probeError struct{ msg string }

func (p *probeError) Error() string { return p.msg }
