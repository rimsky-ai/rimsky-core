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
)

func TestHandler_ListFrames_DerivedStates(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	completedFrame, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/completed")
	endFrame(t, ctx, store, completedFrame)

	failedFrame, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/failed")
	failedRun := seedPendingRun(t, ctx, d, fix.NodeID, failedFrame, fix.MainRunScopeID)
	claimAndPromoteRun(t, ctx, d, failedRun, "sup-1")
	markRunFailed(t, ctx, store, failedRun)
	endFrame(t, ctx, store, failedFrame)

	runningFrame, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/running")

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	states := listFrameStates(t, r, "")
	if states[completedFrame.String()] != "completed" {
		t.Fatalf("completed frame state = %q, want completed", states[completedFrame.String()])
	}
	if states[failedFrame.String()] != "failed" {
		t.Fatalf("failed frame state = %q, want failed", states[failedFrame.String()])
	}
	if states[runningFrame.String()] != "running" {
		t.Fatalf("running frame state = %q, want running", states[runningFrame.String()])
	}

	runningOnly := listFrameStates(t, r, "?state=running")
	if len(runningOnly) != 1 {
		t.Fatalf("?state=running returned %d frames, want 1: %+v", len(runningOnly), runningOnly)
	}
	if _, ok := runningOnly[runningFrame.String()]; !ok {
		t.Fatalf("?state=running did not include the running frame: %+v", runningOnly)
	}

	failedFilter := listFrameStates(t, r, "?state=failed")
	if len(failedFilter) != 2 {
		t.Fatalf("?state=failed returned %d frames, want 2 (conflates failed+completed as not-running): %+v", len(failedFilter), failedFilter)
	}
	if _, ok := failedFilter[completedFrame.String()]; !ok {
		t.Fatalf("?state=failed did not include the completed frame (conflation): %+v", failedFilter)
	}
	if _, ok := failedFilter[failedFrame.String()]; !ok {
		t.Fatalf("?state=failed did not include the failed frame: %+v", failedFilter)
	}
}

func TestHandler_GetFrame_ReturnsDerivedState(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	frameID, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/get")
	endFrame(t, ctx, store, frameID)

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/frames/%s", frameID.String()), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Frame map[string]any `json:"frame"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Frame["state"] != "completed" {
		t.Fatalf("frame state = %v, want completed", body.Frame["state"])
	}
}

func TestHandler_ListFrames_PaginationCursor(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	first, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/first")
	endFrame(t, ctx, store, first)
	second, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/second")
	endFrame(t, ctx, store, second)

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/frames?limit=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var page1 struct {
		Frames     []map[string]any `json:"frames"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatalf("unmarshal page1: %v", err)
	}
	if len(page1.Frames) != 1 {
		t.Fatalf("page1 frames = %d, want 1", len(page1.Frames))
	}
	if page1.NextCursor == "" {
		t.Fatal("page1 next_cursor empty, want a cursor since more rows remain")
	}

	req2 := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/frames?limit=1&cursor=%s", page1.NextCursor), nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w2.Code, w2.Body.String())
	}
	var page2 struct {
		Frames []map[string]any `json:"frames"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("unmarshal page2: %v", err)
	}
	if len(page2.Frames) != 1 {
		t.Fatalf("page2 frames = %d, want 1", len(page2.Frames))
	}
	if page2.Frames[0]["frame_id"] == page1.Frames[0]["frame_id"] {
		t.Fatalf("cursor did not advance: page2 returned the same frame as page1 (%v)", page2.Frames[0]["frame_id"])
	}
}

func listFrameStates(t *testing.T, r http.Handler, query string) map[string]string {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/observability/frames"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Frames []map[string]any `json:"frames"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := map[string]string{}
	for _, f := range body.Frames {
		out[f["frame_id"].(string)] = f["state"].(string)
	}
	return out
}
