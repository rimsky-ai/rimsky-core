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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestHandler_GetNode_PopulatesEnrichment(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	frameID, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/node-enrichment")
	runID := seedPendingRun(t, ctx, d, fix.NodeID, frameID, fix.MainRunScopeID)
	claimAndPromoteRun(t, ctx, d, runID, "sup-1")

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runID, fix.NodeID, map[string]any{"result": "ok"}, tx)
	}); err != nil {
		t.Fatalf("seed attributes: %v", err)
	}

	producerName := "topics-ring"
	seedClaimHandle(t, ctx, store, producerName, fix.NodeID)

	markRunFailed(t, ctx, store, runID)

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/nodes/%s/%s", fix.InstanceID.String(), "fixture-node-type"), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Node             map[string]any   `json:"node"`
		RunSummary       map[string]any   `json:"run_summary"`
		Events           []map[string]any `json:"events"`
		Holdings         []map[string]any `json:"holdings"`
		LatestAttributes map[string]any   `json:"latest_attributes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Node["id"] != fix.NodeID.String() {
		t.Fatalf("node.id = %v, want %s", body.Node["id"], fix.NodeID.String())
	}
	if got := body.RunSummary["failed_count"]; got != float64(1) {
		t.Fatalf("run_summary.failed_count = %v, want 1", got)
	}
	if len(body.Holdings) != 1 || body.Holdings[0]["producer_name"] != producerName {
		t.Fatalf("holdings = %+v, want the seeded claim handle", body.Holdings)
	}
	if body.LatestAttributes["result"] != "ok" {
		t.Fatalf("latest_attributes = %+v, want {result: ok}", body.LatestAttributes)
	}
}

func TestHandler_GetNode_NotFound(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/nodes/%s/%s", fix.InstanceID.String(), "no-such-node-type"), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", w.Code, w.Body.String())
	}
}
