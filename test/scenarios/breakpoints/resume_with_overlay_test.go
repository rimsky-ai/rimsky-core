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

func TestResumeWithOverlay(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "overlay-applied")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-resume-with-overlay", Version: "1",
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

	iid := createInstanceWithPause(t, h, tid, "ck-resume-with-overlay", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	hit := waitForHitOnBreakpoint(t, h, bpID)

	status, out := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id":  hit.ID.String(),
		"overlay": map[string]any{"tag": "overlay-value"},
	})
	require.Equal(t, http.StatusOK, status, "resume should succeed: %v", out)
	require.Equal(t, true, out["first_resume"])

	row := getHitRow(t, h, hit.ID)
	require.NotNil(t, row.ResumeOverlay)
	require.Equal(t, "overlay-value", row.ResumeOverlay["tag"])

	waitForStubObservedCount(h, "worker", 1)
	var seen string
	for _, o := range h.Stub.Observed() {
		if o.NodeType != "worker" {
			continue
		}
		v, _ := o.Attributes["tag"].(string)
		seen = v
		break
	}
	require.Equal(t, "overlay-value", seen,
		"executor's ExecuteRequest.attributes must reflect the L6 overlay merged into the dispatched bag")

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
}
