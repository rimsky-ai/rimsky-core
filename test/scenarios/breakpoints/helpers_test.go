// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// helpers_test.go — shared HTTP+persistence shims for the breakpoint
// end-to-end scenarios per spec §10.2. Each scenario file installs
// breakpoints via the live control-api routes and inspects the
// supervisor's evaluator side-effects (hit rows, dispatch progression)
// through the harness's persistence handle and the stub executor's
// Observed log. All helpers fatal on transport errors so individual
// scenarios stay focused on the breakpoint-shape they're pinning.
//
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

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/scenario"
)

// breakpointCreate POSTs a create-breakpoint body and returns the new
// breakpoint id. Fatals on any non-201 status.
func breakpointCreate(t *testing.T, h *scenario.Harness, instanceID shared.UUID, body map[string]any) shared.UUID {
	t.Helper()
	status, out := postJSON(t, h.ControlBase+fmt.Sprintf("/instances/%s/breakpoints", instanceID.String()), body)
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

// breakpointResume POSTs a resume body and returns the decoded response.
// Fatals when the HTTP layer hiccups; non-200 statuses are returned as
// (status, body) so callers can pin 400s.
func breakpointResume(t *testing.T, h *scenario.Harness, instanceID, breakpointID shared.UUID, body map[string]any) (int, map[string]any) {
	t.Helper()
	url := h.ControlBase + fmt.Sprintf("/instances/%s/breakpoints/%s/resume",
		instanceID.String(), breakpointID.String())
	return postJSON(t, url, body)
}

// breakpointDelete DELETEs the breakpoint row; fatals on non-204.
func breakpointDelete(t *testing.T, h *scenario.Harness, instanceID, breakpointID shared.UUID) {
	t.Helper()
	url := h.ControlBase + fmt.Sprintf("/instances/%s/breakpoints/%s",
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

// instancePause / instanceResume drive the soft-pause endpoints from
// spec §5.1. Both return (status, body) so the rare 409 paths
// (`already paused` / `not paused`) can be asserted explicitly.
func instancePause(t *testing.T, h *scenario.Harness, instanceID shared.UUID) (int, map[string]any) {
	t.Helper()
	return postJSON(t, h.ControlBase+fmt.Sprintf("/instances/%s/pause", instanceID.String()), map[string]any{})
}

func instanceResume(t *testing.T, h *scenario.Harness, instanceID shared.UUID) (int, map[string]any) {
	t.Helper()
	return postJSON(t, h.ControlBase+fmt.Sprintf("/instances/%s/resume", instanceID.String()), map[string]any{})
}

// postJSON marshals body, POSTs to url, decodes response JSON. Returns
// (status, decoded body). When the body fails to decode it returns
// (status, nil). Fatals on transport-level errors.
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
			// Decode failure on a JSON body is unusual — surface for
			// diagnosis. Don't fatal: the caller may be pinning a
			// non-JSON 4xx path.
			t.Logf("postJSON: decode warn (status %d body=%s): %v", resp.StatusCode, string(raw), err)
		}
	}
	return resp.StatusCode, out
}

// waitForHitOnBreakpoint polls until a breakpoint has a hit, returning
// the first hit row. Fatals on timeout. Used by pause-mode scenarios to
// confirm the supervisor reached the checkpoint before issuing a resume.
func waitForHitOnBreakpoint(t *testing.T, h *scenario.Harness, bpID shared.UUID, timeout time.Duration) persistence.BreakpointHitRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
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
	t.Fatalf("waitForHitOnBreakpoint: no hit on breakpoint %s within %v", bpID.String(), timeout)
	return persistence.BreakpointHitRow{}
}

// waitForHitCount polls until len(hits) >= want, returning the slice.
// Used by multi-hit scenarios (multi-breakpoint match, hit-queue
// overflow). Fatals on timeout.
func waitForHitCount(t *testing.T, h *scenario.Harness, bpID shared.UUID, want int, timeout time.Duration) []persistence.BreakpointHitRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var hits []persistence.BreakpointHitRow
	for time.Now().Before(deadline) {
		if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 1000, tx)
			hits = r
			return err
		}); err != nil {
			t.Fatalf("waitForHitCount: %v", err)
		}
		if len(hits) >= want {
			return hits
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("waitForHitCount: bp=%s want=%d got=%d within %v", bpID.String(), want, len(hits), timeout)
	return hits
}

// getBreakpointRow fetches the breakpoint row directly for tests that
// inspect dropped_count / expires_at side-effects.
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

// getHitRow fetches a single hit row by id; nil-safe.
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

// stubObservedCount returns the count of Observed dispatches matching
// the given node_type. Used to assert "no executor call before resume"
// and "exactly one executor call after resume".
func stubObservedCount(h *scenario.Harness, nodeType string) int {
	n := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == nodeType {
			n++
		}
	}
	return n
}

// waitForStubObservedCount blocks until stubObservedCount(nodeType) >= want.
// Returns true on convergence, false on timeout.
func waitForStubObservedCount(h *scenario.Harness, nodeType string, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if stubObservedCount(h, nodeType) >= want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// createInstanceWithPause POSTs to /instances with `paused: true` and
// returns the new instance id. Mirrors scenario.Harness.CreateInstance
// but does NOT call waitForRootDispatch (no dispatch should happen until
// resume).
func createInstanceWithPause(t *testing.T, h *scenario.Harness, templateHash, consumerKey string, params map[string]any) shared.UUID {
	t.Helper()
	bodyMap := map[string]any{
		"template": templateHash,
		"params":   params,
		"paused":   true,
	}
	if consumerKey != "" {
		bodyMap["instance_key"] = consumerKey
	}
	status, out := postJSON(t, h.ControlBase+"/instances", bodyMap)
	if status != http.StatusCreated {
		t.Fatalf("createInstanceWithPause: status %d body=%v", status, out)
	}
	idStr, _ := out["instance_id"].(string)
	id, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatalf("createInstanceWithPause: bad instance_id %q: %v", idStr, err)
	}
	return shared.UUID(id)
}
