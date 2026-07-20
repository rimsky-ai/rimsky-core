// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint
// @concept: event-log

package breakpoints

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestBreakpointHitEmitsEvent(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-hit-event", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-hit-event", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
		"mode":       "notify_only",
	})
	_, _ = instanceResume(t, h, iid)

	hits := waitForHitCount(t, h, bpID, 1)
	require.Len(t, hits, 1)
	hit := hits[0]
	require.Equal(t, bpID, hit.BreakpointID)
	require.Equal(t, "before_dispatch", string(hit.Checkpoint))
	require.Equal(t, "notify_only", string(hit.Mode))

	url := h.ControlBase + "/v1/events?kind=breakpoint.hit&instance_id=" + iid.String()
	events := waitForBreakpointHitEvents(t, url, 1)
	require.Len(t, events, 1,
		"a recorded breakpoint hit must be reflected as exactly one breakpoint.hit event on GET /events")

	payload := events[0].Payload
	require.Equal(t, iid.String(), asString(payload["instance_id"]),
		"event payload must carry the instance_id")
	require.NotEmpty(t, asString(payload["node_id"]),
		"event payload must carry the owning node_id")
	require.Equal(t, bpID.String(), asString(payload["breakpoint_id"]),
		"event payload must carry the breakpoint_id")
	require.Equal(t, hit.ID.String(), asString(payload["hit_id"]),
		"event payload's hit_id must equal the ledger hit's ID")
	require.Equal(t, "before_dispatch", asString(payload["checkpoint"]),
		"event payload must carry the checkpoint")
	require.Equal(t, "notify_only", asString(payload["mode"]),
		"event payload must carry the breakpoint mode")

	var persistedCount int
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		res, err := h.Persist.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &iid,
			KindIn:     []string{"breakpoint.hit"},
		}, persistence.ListPagination{Limit: 1000}, tx)
		if err != nil {
			return err
		}
		persistedCount = len(res.Events)
		return nil
	}))
	require.Equal(t, len(hits), persistedCount,
		"breakpoint.hit event count must equal the ledger hit count — the event is co-transactional with the hit")
}

type breakpointHitEvent struct {
	ID         int64          `json:"id"`
	InstanceID string         `json:"instance_id"`
	NodeID     string         `json:"node_id"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
}

func waitForBreakpointHitEvents(t *testing.T, url string, want int) []breakpointHitEvent {
	t.Helper()
	for {
		events := getBreakpointHitEvents(t, url)
		if len(events) >= want {
			return events
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func getBreakpointHitEvents(t *testing.T, url string) []breakpointHitEvent {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("getBreakpointHitEvents: GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("getBreakpointHitEvents: read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getBreakpointHitEvents: GET %s status %d: %s", url, resp.StatusCode, string(raw))
	}
	var decoded struct {
		Events []breakpointHitEvent `json:"events"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("getBreakpointHitEvents: decode %s: %v", string(raw), err)
	}
	return decoded.Events
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
