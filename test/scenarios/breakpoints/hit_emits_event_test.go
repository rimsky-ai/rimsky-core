// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins acceptance AG-S-observability-breakpoint-hit-event:
//
//   When the supervisor records a breakpoint hit, a `breakpoint.hit`
//   event-log row is appended IN THE SAME TXN as the BreakpointHits
//   ledger row (carrying instance id, node id, breakpoint id, hit id,
//   checkpoint, mode), and a client polling
//   `GET /events?kind=breakpoint.hit` observes it — so a recorded hit
//   is always reflected on `/events`.
//
// notify_only mode is chosen so the dispatch does not block on a resume;
// the supervisor reaches the checkpoint, writes the hit (and — once the
// GREEN pass lands — the co-transactional event), and continues.
//
// PROOF-FIRST RED: against current source no `breakpoint.hit` event kind
// is ever appended, so the `GET /events?kind=breakpoint.hit` poll returns
// empty and the "exactly one event row" assertion fails. A later pass adds
// the co-transactional Append inside the hit-create tx to turn this green.
//
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

	// The supervisor reaches the before_dispatch checkpoint and writes a
	// single hit row (notify_only does not block the dispatch).
	hits := waitForHitCount(t, h, bpID, 1, 15*time.Second)
	require.Len(t, hits, 1)
	hit := hits[0]
	require.Equal(t, bpID, hit.BreakpointID)
	require.Equal(t, "before_dispatch", string(hit.Checkpoint))
	require.Equal(t, "notify_only", string(hit.Mode))

	// A client polling the named acceptance route observes exactly one
	// `breakpoint.hit` event row whose payload carries the full hit
	// descriptor and whose `hit_id` is the ledger hit's stable ID.
	url := h.ControlBase + "/events?kind=breakpoint.hit&instance_id=" + iid.String()
	events := waitForBreakpointHitEvents(t, url, 1, 10*time.Second)
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

	// Txn-coupling: read the same kind straight out of persistence and
	// assert the event-row count equals the ledger hit count (1). This
	// proves the event is written in the SAME tx that creates the hit —
	// a recorded hit is ALWAYS reflected on /events, never lagging or
	// orphaned. (If the Append were on a separate, best-effort path this
	// count could drift below the hit count; pinning equality forbids it.)
	var persistedCount int
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		res, err := h.Persist.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &iid,
			Kind:       "breakpoint.hit",
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

// breakpointHitEvent is the GET /events row projection this scenario reads.
type breakpointHitEvent struct {
	ID         int64          `json:"id"`
	InstanceID string         `json:"instance_id"`
	NodeID     string         `json:"node_id"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
}

// waitForBreakpointHitEvents polls the control-api GET /events route until
// at least `want` rows are returned, then returns the slice. Fatals on
// timeout (the RED state: no rows are ever appended, so this fatals and
// the test fails — exactly the proof-first expectation).
func waitForBreakpointHitEvents(t *testing.T, url string, want int, timeout time.Duration) []breakpointHitEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var events []breakpointHitEvent
	for time.Now().Before(deadline) {
		events = getBreakpointHitEvents(t, url)
		if len(events) >= want {
			return events
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("waitForBreakpointHitEvents: want >=%d rows from %s within %v, got %d",
		want, url, timeout, len(events))
	return events
}

// getBreakpointHitEvents does a single unauthenticated GET against the
// control-api /events route (auth is disabled in the default harness) and
// decodes the `events` array. Fatals on transport / decode errors.
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

// asString coerces a decoded JSON payload value to a string (empty when
// absent or non-string), so the payload assertions read cleanly.
func asString(v any) string {
	s, _ := v.(string)
	return s
}
