// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint
// @concept: event-log
// @story: breakpoint-debugger

package breakpoints

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestBreakpointDebuggerLifecycleE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "debugger-walkthrough")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-debugger-lifecycle", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tag": map[string]any{"type": "string"},
						"ok":  map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-debugger-lifecycle", map[string]any{})

	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})

	listURL := h.ControlBase + fmt.Sprintf("/v1/instances/%s/breakpoints", iid.String())
	listed := listBreakpoints(t, listURL)
	require.Len(t, listed, 1,
		"GET /v1/instances/{id}/breakpoints must return the just-installed breakpoint")
	require.Equal(t, bpID.String(), listed[0]["breakpoint_id"],
		"the listed breakpoint must be the one we installed")
	require.Equal(t, "before_dispatch", listed[0]["checkpoint"],
		"the listed breakpoint must echo the checkpoint we installed")
	require.Equal(t, "pause", listed[0]["mode"],
		"absent `mode` defaults to pause; the list surface must reflect that")

	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	hit := waitForHitOnBreakpoint(t, h, bpID)
	require.Equal(t, bpID, hit.BreakpointID)
	require.Equal(t, "before_dispatch", string(hit.Checkpoint))

	eventsURL := h.ControlBase + "/v1/events?kind=breakpoint.hit&instance_id=" + iid.String()
	events := waitForBreakpointHitEvents(t, eventsURL, 1)
	require.Len(t, events, 1,
		"recorded breakpoint hit must surface on the unified `/v1/events` feed (event-log leg)")
	payload := events[0].Payload
	require.Equal(t, hit.ID.String(), asString(payload["hit_id"]),
		"the unified-feed event's hit_id must equal the ledger hit's id")
	require.Equal(t, bpID.String(), asString(payload["breakpoint_id"]),
		"the unified-feed event must reference the installed breakpoint_id")

	hitsURL := h.ControlBase + fmt.Sprintf("/v1/instances/%s/breakpoint-hits", iid.String())
	hitRows := listBreakpointHits(t, hitsURL)
	require.Len(t, hitRows, 1,
		"recorded breakpoint hit must surface on the dedicated `/v1/instances/{id}/breakpoint-hits` ledger")
	require.Equal(t, hit.ID.String(), hitRows[0]["hit_id"],
		"the breakpoint-hits ledger row must reference the same hit_id")
	require.Equal(t, bpID.String(), hitRows[0]["breakpoint_id"],
		"the breakpoint-hits ledger row must reference the same breakpoint_id")

	var (
		ledgerCount int
		eventCount  int
	)
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		ledgerHits, err := h.Persist.BreakpointHits().ListSinceForInstance(ctx, iid, 0, 1000, tx)
		if err != nil {
			return err
		}
		ledgerCount = len(ledgerHits)
		eventRes, err := h.Persist.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &iid,
			Kind:       "breakpoint.hit",
		}, persistence.ListPagination{Limit: 1000}, tx)
		if err != nil {
			return err
		}
		eventCount = len(eventRes.Events)
		return nil
	}))
	require.Equal(t, 1, ledgerCount, "breakpoint_hits ledger row count must be 1 (the recorded hit)")
	require.Equal(t, ledgerCount, eventCount,
		"breakpoint.hit event-row count must equal the breakpoint_hits ledger row count "+
			"— the event is co-transactional with the hit, never lagging or orphaned")

	resumeStatus, resumeOut := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id":  hit.ID.String(),
		"overlay": map[string]any{"tag": "operator-overlay-value"},
	})
	require.Equal(t, http.StatusOK, resumeStatus,
		"resume with overlay should succeed: %v", resumeOut)
	require.Equal(t, true, resumeOut["first_resume"],
		"the resume response must mark this as the first resume of the hit")

	waitForStubObservedCount(h, "worker", 1)
	var seenTag string
	for _, o := range h.Stub.Observed() {
		if o.NodeType != "worker" {
			continue
		}
		v, _ := o.Attributes["tag"].(string)
		seenTag = v
		break
	}
	require.Equal(t, "operator-overlay-value", seenTag,
		"executor's ExecuteRequest.attributes must carry the overlay merged into the dispatched bag")

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node row must exist after dispatch")
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	latest := waitForLatestAttributesTag(t, h.ControlBase+"/v1/nodes/"+worker.ID.String(),
		"operator-overlay-value")
	require.Equal(t, "operator-overlay-value", latest,
		"GET /v1/nodes/{id} latest_attributes must reflect the overlay applied by the resume "+
			"— this is the operator-visible proof that the next dispatch carried the overlay")

	breakpointDelete(t, h, iid, bpID)

	listed = listBreakpoints(t, listURL)
	require.Empty(t, listed,
		"after DELETE the breakpoint must no longer surface on /v1/instances/{id}/breakpoints")

	require.Eventually(t, func() bool {
		rows := listBreakpointHits(t, hitsURL)
		return len(rows) == 0
	}, 5*time.Second, 50*time.Millisecond,
		"after DELETE the breakpoint-hits ledger surface must be empty — "+
			"no orphan hit rows referencing the deleted breakpoint")

	require.Nil(t, getHitRow(t, h, hit.ID),
		"the persisted hit row must be cascade-deleted along with its parent breakpoint")
}

func listBreakpoints(t *testing.T, url string) []map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("listBreakpoints: GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("listBreakpoints: read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listBreakpoints: GET %s status %d: %s", url, resp.StatusCode, string(raw))
	}
	var decoded struct {
		Breakpoints []map[string]any `json:"breakpoints"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("listBreakpoints: decode %s: %v", string(raw), err)
	}
	return decoded.Breakpoints
}

func listBreakpointHits(t *testing.T, url string) []map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("listBreakpointHits: GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("listBreakpointHits: read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listBreakpointHits: GET %s status %d: %s", url, resp.StatusCode, string(raw))
	}
	var decoded struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("listBreakpointHits: decode %s: %v", string(raw), err)
	}
	return decoded.Hits
}

func waitForLatestAttributesTag(t *testing.T, url, wantTag string) string {
	t.Helper()
	for {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("waitForLatestAttributesTag: GET %s: %v", url, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err == nil {
				if bag, ok := body["latest_attributes"].(map[string]any); ok {
					if v, ok := bag["tag"].(string); ok && v == wantTag {
						return v
					}
				}
			}
		}
		time.Sleep(75 * time.Millisecond)
	}
}

var _ shared.UUID
