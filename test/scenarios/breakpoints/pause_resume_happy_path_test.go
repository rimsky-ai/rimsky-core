// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	iid := createInstanceWithPause(t, h, tid, "ck-pause-resume", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	hit := waitForHitOnBreakpoint(t, h, bpID, 10*time.Second)
	require.NotNil(t, hit.NodeRunID, "hit should carry the node_run_id of the parked dispatch")

	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor should not see the dispatch while paused at the breakpoint")

	status, out := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id": hit.ID.String(),
	})
	require.Equal(t, http.StatusOK, status, "resume should succeed")
	require.Equal(t, true, out["resumed"])
	require.Equal(t, true, out["first_resume"])

	row := getHitRow(t, h, hit.ID)
	require.NotNil(t, row, "hit row should still exist post-resume")
	require.NotNil(t, row.ResumedAt, "resumed_at should be stamped after resume")
	require.Nil(t, row.ResumeOverlay, "no overlay supplied → resume_overlay stays nil")

	require.True(t, waitForStubObservedCount(h, "worker", 1, 10*time.Second),
		"stub should observe the worker dispatch after resume")
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh after the executor returns terminal/success")
}
