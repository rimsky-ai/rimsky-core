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

func TestResumeInvalidOverlay(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "valid-after-retry")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-resume-invalid-overlay", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tag": map[string]any{"type": "string"},
						"ok":  map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-resume-invalid-overlay", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	hit := waitForHitOnBreakpoint(t, h, bpID)

	badStatus, badOut := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id":  hit.ID.String(),
		"overlay": map[string]any{"tag": 42},
	})
	require.Equal(t, http.StatusBadRequest, badStatus,
		"invalid overlay should yield 400 ErrResumeOverlayInvalid; got body=%v", badOut)

	row := getHitRow(t, h, hit.ID)
	require.NotNil(t, row)
	require.Nil(t, row.ResumedAt,
		"hit must stay unresumed after a rejected overlay (rejection is non-destructive)")
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not be called while the hit is still paused")

	goodStatus, goodOut := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id":  hit.ID.String(),
		"overlay": map[string]any{"tag": "good"},
	})
	require.Equal(t, http.StatusOK, goodStatus, "valid retry should succeed: %v", goodOut)
	require.Equal(t, true, goodOut["first_resume"],
		"the second resume IS the first successful one — prior failure didn't write resumed_at")

	waitForStubObservedCount(h, "worker", 1)
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
}
