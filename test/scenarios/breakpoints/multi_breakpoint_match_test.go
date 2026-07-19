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
	bp1 := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	bp2 := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"executor": "stub"},
	})
	_, _ = instanceResume(t, h, iid)

	hit1 := waitForHitOnBreakpoint(t, h, bp1)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not be called while paused at the first breakpoint")

	status, _ := breakpointResume(t, h, iid, bp1, map[string]any{"hit_id": hit1.ID.String()})
	require.Equal(t, http.StatusOK, status)

	hit2 := waitForHitOnBreakpoint(t, h, bp2)
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must remain uncalled while paused at the second breakpoint")

	status, _ = breakpointResume(t, h, iid, bp2, map[string]any{"hit_id": hit2.ID.String()})
	require.Equal(t, http.StatusOK, status)

	waitForStubObservedCount(h, "worker", 1)
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
}
