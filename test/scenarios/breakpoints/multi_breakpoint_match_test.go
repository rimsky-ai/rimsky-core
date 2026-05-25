// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Multi-breakpoint match":
//
//   1. Install two pause-mode breakpoints with overlapping matchers
//      (both match the same node_type).
//   2. The supervisor reaches the before_dispatch checkpoint and the
//      evaluator iterates both breakpoints in order, writing a hit row
//      per match.
//   3. The dispatch blocks at the first breakpoint until resumed; once
//      resumed, the evaluator moves to the second breakpoint, writes
//      its hit row, blocks again until resumed.
//   4. Two resume calls are required; only after the second does the
//      dispatch proceed to the executor.
//
// This pins the per-iteration block + ordered evaluation contract of
// EvaluateBreakpoints. (`ListForInstance` orders by created_at ASC, so
// the first-installed breakpoint fires first.)
//
// @concept: breakpoint

package breakpoints

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestMultiBreakpointMatch(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-multi-match", Version: "1",
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

	iid := createInstanceWithPause(t, h, tid, "ck-multi-match", map[string]any{})
	// Two breakpoints with overlapping matchers — both fire on the same
	// dispatch. Created sequentially so the ListForInstance ordering is
	// deterministic.
	bp1 := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	bp2 := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"executor": "stub"},
	})
	_, _ = instanceResume(t, h, iid)

	// The first hit lands. The evaluator is blocked there.
	hit1 := waitForHitOnBreakpoint(t, h, bp1, 10*time.Second)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not be called while paused at the first breakpoint")

	// Resume bp1; the evaluator should advance to bp2 and write its hit.
	status, _ := breakpointResume(t, h, iid, bp1, map[string]any{"hit_id": hit1.ID.String()})
	require.Equal(t, http.StatusOK, status)

	hit2 := waitForHitOnBreakpoint(t, h, bp2, 10*time.Second)
	// Still no executor — bp2 is now holding the dispatch.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must remain uncalled while paused at the second breakpoint")

	// Resume bp2; the executor should now receive the dispatch.
	status, _ = breakpointResume(t, h, iid, bp2, map[string]any{"hit_id": hit2.ID.String()})
	require.Equal(t, http.StatusOK, status)

	require.True(t, waitForStubObservedCount(h, "worker", 1, 10*time.Second),
		"executor should observe dispatch after both breakpoints resume")
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh after both resumes")
}
