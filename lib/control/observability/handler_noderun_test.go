// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	sqlitedriver "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

func TestHandler_GetNodeRun_Enrichment(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	frameID, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/noderun")
	runID := seedPendingRun(t, ctx, d, fix.NodeID, frameID, fix.MainRunScopeID)
	claimAndPromoteRun(t, ctx, d, runID, "sup-1")

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/node-runs/%s", runID.String()), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["instance_id"] != fix.InstanceID.String() {
		t.Fatalf("instance_id = %v, want %s (enrichment via node lookup)", body["instance_id"], fix.InstanceID.String())
	}
	if body["node_type"] != "fixture-node-type" {
		t.Fatalf("node_type = %v, want fixture-node-type", body["node_type"])
	}
	if body["state"] != "running" {
		t.Fatalf("state = %v, want the real cascade state (running) now that a claim was promoted, not the collapsed pending/claimed bucket", body["state"])
	}
}

func TestHandler_GetNodeRun_DistinguishesHeldFromRunning(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	frameID, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/noderun")
	runID := seedPendingRun(t, ctx, d, fix.NodeID, frameID, fix.MainRunScopeID)
	claimAndPromoteRun(t, ctx, d, runID, "sup-1")

	rawDB := sqlitedriver.DBFromDatabase(d)
	if _, err := rawDB.ExecContext(ctx, `UPDATE rimsky_node_runs SET state = 'held' WHERE id = ?`, runID.String()); err != nil {
		t.Fatalf("force run to held: %v", err)
	}

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/node-runs/%s", runID.String()), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["state"] != "held" {
		t.Fatalf("state = %v, want held (a held run must be distinguishable from a running one, not collapsed into a shared 'claimed' bucket)", body["state"])
	}
}

func TestHandler_ListNodeRuns_RejectsUnsupportedStateFilter(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/node-runs?state=held", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a ?state= value the live-queue filter cannot express (only pending/claimed are supported); body = %s", w.Code, w.Body.String())
	}
}

func TestHandler_GetNodeRun_NotFound(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/node-runs/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", w.Code, w.Body.String())
	}
}
