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

func TestBreakpointFiresOnAttributeLessNode_NotifyOnly(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, false, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-attrless-notify", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-attrless-notify", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
		"mode":       "notify_only",
	})
	_, _ = instanceResume(t, h, iid)

	hits := waitForHitCount(t, h, bpID, 1)
	require.Len(t, hits, 1)
	require.Equal(t, "before_dispatch", string(hits[0].Checkpoint))
	require.Equal(t, "notify_only", string(hits[0].Mode))

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
}

func TestBreakpointFiresOnAttributeLessNode_PauseBlocks(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, false, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-attrless-pause", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-attrless-pause", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	hit := waitForHitOnBreakpoint(t, h, bpID)

	h.WaitForSchedulerQuiescence()
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"pause-mode breakpoint must block the attribute-less dispatch before Execute")

	status, out := breakpointResume(t, h, iid, bpID, map[string]any{"hit_id": hit.ID.String()})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, out["resumed"])

	waitForStubObservedCount(h, "worker", 1)
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
}
