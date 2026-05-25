// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Pause-and-resume happy path":
//
//   1. Operator installs a pause-mode breakpoint on a live instance
//      via POST /instances/{id}/breakpoints.
//   2. The supervisor's first dispatch of the matching node lands at
//      the before_dispatch checkpoint, writes a hit row, and blocks
//      inside waitForResume.
//   3. The agent reads the hit (modeled here as a direct persistence
//      query — the MCP read path is exercised by the MCP-resources
//      scenario tests separately) and POSTs the resume call without
//      an overlay.
//   4. The supervisor's polling loop sees `resumed_at != NULL`, returns
//      from EvaluateBreakpoints, and proceeds to dispatch the executor.
//   5. The stub emits a terminal/success and the node reaches Fresh.
//
// The key assertions are timing-ordered:
//   - Before the resume call, the stub has observed zero dispatches for
//     the `worker` node (the breakpoint is holding it).
//   - After resume, the stub observes exactly one dispatch and the node
//     transitions to terminal/success.
//
// @concept: breakpoint

package breakpoints

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

func TestPauseResumeHappyPath(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "happy")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-pause-resume", Version: "1",
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

	// Install the pause-mode breakpoint BEFORE creating the instance so
	// the breakpoint is already present when the supervisor reaches the
	// before_dispatch checkpoint. The breakpoint matches the worker by
	// node_type — the cleanest "fire on this dispatch" predicate.
	//
	// Order matters here: CreateInstance kicks off the dispatch loop,
	// so the breakpoint MUST be created against an existing instance.
	// We solve this by creating the instance paused, installing the
	// breakpoint while the instance is held, then resuming the instance
	// via /instances/{id}/resume so dispatch starts with the breakpoint
	// already in place.
	iid := createInstanceWithPause(t, h, tid, "ck-pause-resume", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	// Wait for the hit row to appear. This proves the supervisor reached
	// the breakpoint and is parked inside waitForResume.
	hit := waitForHitOnBreakpoint(t, h, bpID, 10*time.Second)
	require.NotNil(t, hit.NodeRunID, "hit should carry the node_run_id of the parked dispatch")

	// Before resume, the executor must NOT have been called — the
	// before_dispatch checkpoint fires before Execute. A short wait
	// guards against a racing dispatch that was already in flight.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor should not see the dispatch while paused at the breakpoint")

	// Resume without an overlay. First call → first_resume:true.
	status, out := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id": hit.ID.String(),
	})
	require.Equal(t, http.StatusOK, status, "resume should succeed")
	require.Equal(t, true, out["resumed"])
	require.Equal(t, true, out["first_resume"])

	// The persisted hit row should now carry resumed_at and no overlay.
	row := getHitRow(t, h, hit.ID)
	require.NotNil(t, row, "hit row should still exist post-resume")
	require.NotNil(t, row.ResumedAt, "resumed_at should be stamped after resume")
	require.Nil(t, row.ResumeOverlay, "no overlay supplied → resume_overlay stays nil")

	// Supervisor's poll cadence is 250ms; the dispatch should reach the
	// executor and the node should transition to Fresh well within 10s.
	require.True(t, waitForStubObservedCount(h, "worker", 1, 10*time.Second),
		"stub should observe the worker dispatch after resume")
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh after the executor returns terminal/success")
}
