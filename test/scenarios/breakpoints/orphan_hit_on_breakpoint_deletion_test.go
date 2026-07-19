// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package breakpoints

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
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

	hit := waitForHitOnBreakpoint(t, h, bpID)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not see the dispatch while paused at the breakpoint")

	breakpointDelete(t, h, iid, bpID)

	require.Nil(t, getHitRow(t, h, hit.ID),
		"hit row should be cascade-deleted along with the parent breakpoint")

	waitForStubObservedCount(h, "worker", 1)
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
}
