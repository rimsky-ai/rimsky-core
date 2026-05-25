// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Orphan hit on breakpoint deletion":
//
//   1. Install a pause-mode breakpoint.
//   2. The supervisor's dispatch hits the breakpoint and parks inside
//      waitForResume.
//   3. The operator deletes the breakpoint row — the FK ON DELETE
//      CASCADE on rimsky_breakpoint_hits.breakpoint_id removes the
//      parked hit row as well.
//   4. The waitForResume poll sees `Get(hitID) == nil` and returns
//      with no overlay; the dispatch proceeds (treated as auto-resume).
//   5. The executor receives the dispatch and reaches terminal.
//
// The key correctness property is that the runner does NOT deadlock on
// the deleted hit. Pre-Pass-5 the loop would have spun forever waiting
// for a row that never returns. The Pass-5 wiring `if hit == nil { return
// nil, nil }` is what this scenario exercises end-to-end.
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

func TestOrphanHitOnBreakpointDeletion(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "orphaned-and-released")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-orphan-hit-on-deletion", Version: "1",
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

	iid := createInstanceWithPause(t, h, tid, "ck-orphan-hit-on-deletion", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	hit := waitForHitOnBreakpoint(t, h, bpID, 10*time.Second)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not see the dispatch while paused at the breakpoint")

	// Delete the breakpoint (cascade-deletes the hit). The
	// waitForResume poll inside the parked runner will see the row
	// vanish and return as if auto-resumed.
	breakpointDelete(t, h, iid, bpID)

	// Hit row must be gone (FK CASCADE).
	require.Nil(t, getHitRow(t, h, hit.ID),
		"hit row should be cascade-deleted along with the parent breakpoint")

	// The dispatch should proceed without an explicit resume call.
	require.True(t, waitForStubObservedCount(h, "worker", 1, 10*time.Second),
		"executor should observe dispatch after the orphan-hit unblocks the runner")
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh once the runner unblocks via orphan-hit path")
}
