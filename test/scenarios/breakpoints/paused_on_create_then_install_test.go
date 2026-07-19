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

	iid := createInstanceWithPause(t, h, tid, "ck-paused-on-create", map[string]any{})

	time.Sleep(500 * time.Millisecond)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"paused-on-create instance must not be dispatched before resume")

	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	time.Sleep(300 * time.Millisecond)
	row := getBreakpointRow(t, h, bpID)
	require.NotNil(t, row)

	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	hit := waitForHitOnBreakpoint(t, h, bpID)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not be called between instance-resume and breakpoint-resume")

	status, _ = breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id": hit.ID.String(),
	})
	require.Equal(t, http.StatusOK, status)

	waitForStubObservedCount(h, "worker", 1)
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
}
