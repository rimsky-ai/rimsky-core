// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

// @concept: cascade-graph
// @story: frame-origin-audit
func TestHandler_FrameRoutesCarryTheTriggeringMessage(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))
	frameID, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/frame-origin")

	disc := observability.NewDiscovery(&nopProber{})
	r := newRouter(t, observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc})

	req := httptest.NewRequest("GET", "/v1/observability/frames", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list frames = %d, body=%s", w.Code, w.Body.String())
	}
	var list struct {
		Frames []map[string]any `json:"frames"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	var listed map[string]any
	for _, f := range list.Frames {
		if f["frame_id"] == frameID.String() {
			listed = f
		}
	}
	if listed == nil {
		t.Fatalf("frame %s missing from the list: %+v", frameID, list.Frames)
	}
	assertCarriesMessage(t, "list", listed, "test/frame-origin")

	req = httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/frames/%s", frameID.String()), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get frame = %d, body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Frame map[string]any `json:"frame"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	assertCarriesMessage(t, "get", got.Frame, "test/frame-origin")
}

func assertCarriesMessage(t *testing.T, route string, frame map[string]any, wantType string) {
	t.Helper()
	if frame["message_type"] != wantType {
		t.Errorf("%s frame route: message_type = %v, want %q — the dashboard's frame routes join the "+
			"triggering message, matching the instance-scoped routes", route, frame["message_type"], wantType)
	}
	if s, _ := frame["message_sender"].(string); s == "" {
		t.Errorf("%s frame route: message_sender empty", route)
	}
	if k, _ := frame["message_sender_kind"].(string); k == "" {
		t.Errorf("%s frame route: message_sender_kind empty", route)
	}
}
