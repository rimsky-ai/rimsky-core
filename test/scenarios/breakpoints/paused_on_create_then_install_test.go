// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Paused-on-create + install + release":
//
//   1. POST /instances with `paused: true` → instance row created with
//      rimsky_instances.paused = true; supervisor candidate-selection
//      skips it.
//   2. Install a pause-mode breakpoint on the held instance. The
//      breakpoint exists but no dispatch has fired (the instance is
//      held by the paused flag, not by the breakpoint).
//   3. POST /instances/{id}/resume → the supervisor's next tick picks
//      the instance up, dispatches the root frame, and the first
//      dispatch hits the breakpoint.
//   4. Confirm the executor was never called before the release. The
//      pre-release dispatch is held by paused=true, the post-release
//      dispatch is held by the breakpoint, so the executor's first call
//      is post-resume of the breakpoint hit.
//
// This pins the composition of soft-pause (instance-level hold) with
// runtime breakpoints (per-dispatch hold) — they are orthogonal gates,
// both of which must release before the executor sees the dispatch.
//
// @concept: breakpoint

package breakpoints

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestPausedOnCreateThenInstall(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-paused-on-create", Version: "1",
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

	// @deliberate: Step 1: create paused. No dispatch should fire — the candidate-
	// selection filter holds the instance.
	iid := createInstanceWithPause(t, h, tid, "ck-paused-on-create", map[string]any{})

	// @constraint: Give the supervisor a tick to (not) pick up the instance, then
	// assert nothing was dispatched.
	time.Sleep(500 * time.Millisecond)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"paused-on-create instance must not be dispatched before resume")

	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	// @deliberate: Sanity: no hit yet — there has been no dispatch.
	time.Sleep(300 * time.Millisecond)
	row := getBreakpointRow(t, h, bpID)
	require.NotNil(t, row)

	// @deliberate: Step 3: release the soft-pause. The supervisor picks up the
	// instance, dispatches the worker, and the breakpoint catches it.
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	hit := waitForHitOnBreakpoint(t, h, bpID, 10*time.Second)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not be called between instance-resume and breakpoint-resume")

	status, _ = breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id": hit.ID.String(),
	})
	require.Equal(t, http.StatusOK, status)

	require.True(t, waitForStubObservedCount(h, "worker", 1, 10*time.Second),
		"executor should see dispatch after both gates open")
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh once both gates release")
}
