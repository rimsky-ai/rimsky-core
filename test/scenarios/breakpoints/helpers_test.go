// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint

package breakpoints

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func breakpointCreate(t *testing.T, h *scenario.Harness, instanceID shared.UUID, body map[string]any) shared.UUID {
	t.Helper()
	status, out := postJSON(t, h.ControlBase+fmt.Sprintf("/v1/instances/%s/breakpoints", instanceID.String()), body)
	if status != http.StatusCreated {
		t.Fatalf("breakpointCreate: status %d body=%v", status, out)
	}
	idStr, _ := out["breakpoint_id"].(string)
	if idStr == "" {
		t.Fatalf("breakpointCreate: empty breakpoint_id in response: %v", out)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatalf("breakpointCreate: bad breakpoint_id %q: %v", idStr, err)
	}
	return id
}

func breakpointResume(t *testing.T, h *scenario.Harness, instanceID, breakpointID shared.UUID, body map[string]any) (int, map[string]any) {
	t.Helper()
	url := h.ControlBase + fmt.Sprintf("/v1/instances/%s/breakpoints/%s/resume",
		instanceID.String(), breakpointID.String())
	return postJSON(t, url, body)
}

func breakpointDelete(t *testing.T, h *scenario.Harness, instanceID, breakpointID shared.UUID) {
	t.Helper()
	url := h.ControlBase + fmt.Sprintf("/v1/instances/%s/breakpoints/%s",
		instanceID.String(), breakpointID.String())
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("breakpointDelete: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("breakpointDelete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("breakpointDelete: status %d: %s", resp.StatusCode, string(buf))
	}
}

func instancePause(t *testing.T, h *scenario.Harness, instanceID shared.UUID) (int, map[string]any) {
	t.Helper()
	return postJSON(t, h.ControlBase+fmt.Sprintf("/v1/instances/%s/pause", instanceID.String()), map[string]any{})
}

func instanceResume(t *testing.T, h *scenario.Harness, instanceID shared.UUID) (int, map[string]any) {
	t.Helper()
	return postJSON(t, h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", instanceID.String()), map[string]any{})
}

func postJSON(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("postJSON: marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("postJSON: read: %v", err)
	}
	var out map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Logf("postJSON: decode warn (status %d body=%s): %v", resp.StatusCode, string(raw), err)
		}
	}
	return resp.StatusCode, out
}

func waitForHitOnBreakpoint(t *testing.T, h *scenario.Harness, bpID shared.UUID) persistence.BreakpointHitRow {
	t.Helper()
	for {
		var hits []persistence.BreakpointHitRow
		if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 100, tx)
			hits = r
			return err
		}); err != nil {
			t.Fatalf("waitForHitOnBreakpoint: %v", err)
		}
		if len(hits) > 0 {
			return hits[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func listHitsForBreakpoint(t *testing.T, h *scenario.Harness, bpID shared.UUID) []persistence.BreakpointHitRow {
	t.Helper()
	var hits []persistence.BreakpointHitRow
	if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 1000, tx)
		hits = r
		return err
	}); err != nil {
		t.Fatalf("listHitsForBreakpoint: %v", err)
	}
	return hits
}

func waitForHitCount(t *testing.T, h *scenario.Harness, bpID shared.UUID, want int) []persistence.BreakpointHitRow {
	t.Helper()
	for {
		hits := listHitsForBreakpoint(t, h, bpID)
		if len(hits) >= want {
			return hits
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func getBreakpointRow(t *testing.T, h *scenario.Harness, bpID shared.UUID) *persistence.BreakpointRow {
	t.Helper()
	var out *persistence.BreakpointRow
	if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.Breakpoints().Get(ctx, bpID, tx)
		out = r
		return err
	}); err != nil {
		t.Fatalf("getBreakpointRow: %v", err)
	}
	return out
}

func getHitRow(t *testing.T, h *scenario.Harness, hitID shared.UUID) *persistence.BreakpointHitRow {
	t.Helper()
	var out *persistence.BreakpointHitRow
	if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.BreakpointHits().Get(ctx, hitID, tx)
		out = r
		return err
	}); err != nil {
		t.Fatalf("getHitRow: %v", err)
	}
	return out
}

func stubObservedCount(h *scenario.Harness, nodeType string) int {
	n := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == nodeType {
			n++
		}
	}
	return n
}

func waitForStubObservedCount(h *scenario.Harness, nodeType string, want int) {
	for stubObservedCount(h, nodeType) < want {
		time.Sleep(20 * time.Millisecond)
	}
}

// @decision: empty-message-as-root-trigger
// @decision: test-harness-create-instance-wakes-roots-after-create
func createInstanceWithPause(t *testing.T, h *scenario.Harness, templateHash, consumerKey string, params map[string]any) shared.UUID {
	t.Helper()
	bodyMap := map[string]any{
		"template":     templateHash,
		"params":       params,
		"paused":       true,
		"target_agent": "scenario-default-agent",
	}
	if consumerKey != "" {
		bodyMap["instance_key"] = consumerKey
	}
	status, out := postJSON(t, h.ControlBase+"/v1/instances", bodyMap)
	if status != http.StatusCreated {
		t.Fatalf("createInstanceWithPause: status %d body=%v", status, out)
	}
	idStr, _ := out["instance_id"].(string)
	id, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatalf("createInstanceWithPause: bad instance_id %q: %v", idStr, err)
	}
	instID := shared.UUID(id)
	h.PostInstanceMessage(instID, "", nil, "bp-wake-"+instID.String())
	return instID
}
