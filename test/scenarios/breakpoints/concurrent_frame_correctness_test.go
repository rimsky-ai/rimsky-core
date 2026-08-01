// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func TestConcurrentFrameCorrectness(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker_a").Success(map[string]any{"a_ok": true}, true, "a")
	h.Stub.WhenType("worker_b").Success(map[string]any{"b_ok": true}, true, "b")

	openSchema := func(field string) map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				field: map[string]any{"type": "boolean", "readOnly": true},
			},
		}
	}

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-concurrent-frame", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker_a", Executor: "stub"},
				scenario.WithAttributes(openSchema("a_ok")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker_b", Executor: "stub"},
				scenario.WithAttributes(openSchema("b_ok")),
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-concurrent-frame", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker_a"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	hit := waitForHitOnBreakpoint(t, h, bpID)

	nb := h.FindNode(iid, "worker_b")
	require.NotNil(t, nb)
	h.WaitForNodeState(nb.ID, cascade.NodeStateFresh)
	require.GreaterOrEqual(t, stubObservedCount(h, "worker_b"), 1,
		"executor must observe worker_b dispatch during the pause window")
	require.Equal(t, 0, stubObservedCount(h, "worker_a"),
		"executor must NOT observe worker_a while paused at the breakpoint")

	status, _ = breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id": hit.ID.String(),
	})
	require.Equal(t, http.StatusOK, status)

	na := h.FindNode(iid, "worker_a")
	require.NotNil(t, na)
	h.WaitForNodeState(na.ID, cascade.NodeStateFresh)
	require.GreaterOrEqual(t, stubObservedCount(h, "worker_a"), 1,
		"executor must observe worker_a dispatch post-resume")
}
