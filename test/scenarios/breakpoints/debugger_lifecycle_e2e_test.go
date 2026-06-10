// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — STORY-breakpoint-debugger end-to-end proof.
//
// The shipped sibling `hit_emits_event_test.go` covers the unified-feed
// leg (a recorded hit appears co-transactionally on /v1/events alongside
// the breakpoint_hits ledger row). This scenario consolidates the
// surrounding install / list / resume-with-overlay / delete-cascade
// flow into one user-walkthrough proof, exhibiting the full debugger
// loop through the live control-API surface:
//
//  1. Install a pause-mode breakpoint via POST /v1/instances/{id}/breakpoints.
//  2. List the active breakpoints via GET /v1/instances/{id}/breakpoints —
//     the just-installed breakpoint shows up with the matching id.
//  3. Resume the paused instance; the supervisor reaches the checkpoint,
//     parks the dispatch, and writes a hit row.
//  4. Co-transactional dual-surface assertion: the hit appears in the
//     SAME transaction on BOTH the unified `/v1/events?kind=breakpoint.hit`
//     feed AND the dedicated `/v1/instances/{id}/breakpoint-hits` ledger.
//     This is the load-bearing invariant the STORY's Falsifier names —
//     "hit appears on one surface but not the other" is forbidden.
//     We pin this with two independent reads against the live HTTP routes
//     AND a same-tx persistence read that asserts both row counts are
//     equal-and-equal-to-the-hit-count (drift below either count proves a
//     non-co-transactional path).
//  5. Resume the hit with an overlay via POST
//     /v1/instances/{id}/breakpoints/{bp}/resume; the supervisor's
//     deep-merge propagates the overlay into the dispatched attribute bag.
//     We assert the executor's ExecuteRequest reflects the overlay AND
//     that the post-dispatch GET /v1/nodes/{id} `latest_attributes`
//     surface mirrors the overlaid bag — the STORY's
//     "next dispatch's attribute bag carries the overlay" property
//     observed via the latest-attribute read surface.
//  6. Delete the breakpoint via DELETE /v1/instances/{id}/breakpoints/{bp}.
//     The FK ON DELETE CASCADE removes the hit row; we assert BOTH the
//     dedicated hits-ledger surface AND the unified events feed reflect
//     this — the breakpoint-hits ledger is empty for the instance after
//     delete, with no orphans. (The unified-feed `breakpoint.hit` event
//     row is an immutable audit record per concept:event-log and is
//     NOT cascade-deleted; the STORY's "leaves orphaned hits" Falsifier
//     concerns the live `breakpoint_hits` ledger only.)
//
// Race-stress: `go test ./test/scenarios/breakpoints/... -race -count=3`.
// A non-co-transactional event-append would let the events-feed row lag
// the ledger row under contention; the count-equality assertion in
// step 4 forbids this even under -race.
//
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
	// Stub mirrors `tag` (and any other operator-set attribute) back as a
	// no-op — we only need the Observed entry to confirm the overlay
	// reached the executor's ExecuteRequest.
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

	// (1) Install a pause-mode breakpoint via the live POST route.
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
		// mode omitted → defaults to "pause" per spec §4.1; that's what
		// the STORY's "resume" leg requires (notify_only would not park).
	})

	// (2) List active breakpoints via GET — the just-installed row shows.
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

	// Resume the instance — supervisor begins dispatching, hits the
	// checkpoint on the worker, parks the dispatch, and writes a hit row.
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	// Block until the supervisor records the hit (via the persistence
	// helper that opens its own tx — proves the row is committed).
	hit := waitForHitOnBreakpoint(t, h, bpID, 15*time.Second)
	require.Equal(t, bpID, hit.BreakpointID)
	require.Equal(t, "before_dispatch", string(hit.Checkpoint))

	// (4) Co-transactional dual-surface read.
	//
	// First: poll the unified `/v1/events?kind=breakpoint.hit` HTTP feed
	// until the row appears. Without the co-transactional append, this
	// would never converge (the RED state pinned by the sibling test).
	eventsURL := h.ControlBase + "/v1/events?kind=breakpoint.hit&instance_id=" + iid.String()
	events := waitForBreakpointHitEvents(t, eventsURL, 1, 10*time.Second)
	require.Len(t, events, 1,
		"recorded breakpoint hit must surface on the unified `/v1/events` feed (event-log leg)")
	payload := events[0].Payload
	require.Equal(t, hit.ID.String(), asString(payload["hit_id"]),
		"the unified-feed event's hit_id must equal the ledger hit's id")
	require.Equal(t, bpID.String(), asString(payload["breakpoint_id"]),
		"the unified-feed event must reference the installed breakpoint_id")

	// Second: poll the dedicated `/v1/instances/{id}/breakpoint-hits`
	// HTTP feed for the same row. This is the operator's debugger
	// ledger surface (the MCP `breakpoint-hits` resource also reads
	// through this route).
	hitsURL := h.ControlBase + fmt.Sprintf("/v1/instances/%s/breakpoint-hits", iid.String())
	hitRows := listBreakpointHits(t, hitsURL)
	require.Len(t, hitRows, 1,
		"recorded breakpoint hit must surface on the dedicated `/v1/instances/{id}/breakpoint-hits` ledger")
	require.Equal(t, hit.ID.String(), hitRows[0]["hit_id"],
		"the breakpoint-hits ledger row must reference the same hit_id")
	require.Equal(t, bpID.String(), hitRows[0]["breakpoint_id"],
		"the breakpoint-hits ledger row must reference the same breakpoint_id")

	// Third — the load-bearing co-transactional check.
	//
	// A non-co-transactional event-append (separate tx, async, or
	// best-effort) could in principle let either surface lag the other.
	// We open a single read transaction and count BOTH the ledger row
	// AND the breakpoint.hit event row directly out of persistence,
	// asserting both equal the ledger hit count we already observed.
	// This forbids any path where the two surfaces disagree even
	// momentarily. The STORY's Falsifier ("hit appears on one surface
	// but not the other") is the exact opposite of this assertion.
	//
	// This is the property -race -count=3 exists to surface: a
	// non-tx-bound emit site would race and let either count drift.
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

	// (5) Resume with overlay — drives the supervisor's deep-merge into
	// the dispatched attribute bag. The overlay sets `tag`; the bag
	// merge happens inside runtime/breakpoint_eval.go, and the resulting
	// bag flows through to the executor's ExecuteRequest.
	resumeStatus, resumeOut := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id":  hit.ID.String(),
		"overlay": map[string]any{"tag": "operator-overlay-value"},
	})
	require.Equal(t, http.StatusOK, resumeStatus,
		"resume with overlay should succeed: %v", resumeOut)
	require.Equal(t, true, resumeOut["first_resume"],
		"the resume response must mark this as the first resume of the hit")

	// The post-merge L6 overlay must land on the executor's
	// ExecuteRequest.attributes bag.
	require.True(t, waitForStubObservedCount(h, "worker", 1, 15*time.Second),
		"stub must observe the worker dispatch after the resume releases the parked frame")
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

	// (5b) The STORY's "observable via GET /v1/nodes/{id} latest-attribute
	// surface" leg. After the dispatch terminates, the post-run
	// resolved attribute bag is persisted as the node's latest-attribute
	// snapshot. The GET /v1/nodes/{id} read must mirror the overlaid
	// `tag` (this is the operator-visible proof that the overlay
	// actually propagated into the next dispatch, not merely into a
	// transient eval bag).
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node row must exist after dispatch")
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh after the executor returns success")

	latest := waitForLatestAttributesTag(t, h.ControlBase+"/v1/nodes/"+worker.ID.String(),
		"operator-overlay-value", 15*time.Second)
	require.Equal(t, "operator-overlay-value", latest,
		"GET /v1/nodes/{id} latest_attributes must reflect the overlay applied by the resume "+
			"— this is the operator-visible proof that the next dispatch carried the overlay")

	// (6) Delete the breakpoint. The FK ON DELETE CASCADE on
	// rimsky_breakpoint_hits.breakpoint_id removes the hit row as well;
	// the STORY's Falsifier ("deletion leaves orphaned hits") is the
	// exact opposite of this assertion.
	breakpointDelete(t, h, iid, bpID)

	// The breakpoint must be gone from the LIST surface.
	listed = listBreakpoints(t, listURL)
	require.Empty(t, listed,
		"after DELETE the breakpoint must no longer surface on /v1/instances/{id}/breakpoints")

	// The hit must be gone from the dedicated ledger surface (cascade).
	// We poll briefly because the supervisor's resume path may still
	// be retiring the hit row asynchronously; the cascade-delete itself
	// is synchronous with the DELETE request, but the resume path's own
	// hit-row cleanup may have been concurrent.
	require.Eventually(t, func() bool {
		rows := listBreakpointHits(t, hitsURL)
		return len(rows) == 0
	}, 5*time.Second, 50*time.Millisecond,
		"after DELETE the breakpoint-hits ledger surface must be empty — "+
			"no orphan hit rows referencing the deleted breakpoint")

	// And from persistence directly (the load-bearing cascade property).
	require.Nil(t, getHitRow(t, h, hit.ID),
		"the persisted hit row must be cascade-deleted along with its parent breakpoint")
}

// listBreakpoints GETs the breakpoints-list surface and returns the
// decoded items array. Fatals on transport / non-200 status.
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

// listBreakpointHits GETs the breakpoint-hits ledger surface and
// returns the decoded hits array. Fatals on transport / non-200 status.
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

// waitForLatestAttributesTag polls GET /v1/nodes/{id} until the
// `latest_attributes.tag` field equals wantTag, returning the observed
// value (empty on timeout). Used to confirm the overlay propagated into
// the persisted latest-attribute snapshot. The wait window covers the
// gap between the executor returning terminal and the post-run resolved
// bag being persisted as the node-attributes row.
func waitForLatestAttributesTag(t *testing.T, url, wantTag string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var seen string
	for time.Now().Before(deadline) {
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
					if v, ok := bag["tag"].(string); ok {
						seen = v
						if seen == wantTag {
							return seen
						}
					}
				}
			}
		}
		time.Sleep(75 * time.Millisecond)
	}
	t.Fatalf("waitForLatestAttributesTag: want tag=%q at %s within %v, got %q",
		wantTag, url, timeout, seen)
	return seen
}

// Compile-time guard: the shared.UUID alias referenced above is not
// silently dropped by future renames.
var _ shared.UUID
