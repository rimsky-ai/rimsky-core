// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Resume-with-overlay":
//
//   1. Install a pause-mode breakpoint.
//   2. Resume the hit with an overlay that mutates an attribute that
//      flows through to the executor's ExecuteRequest.attributes bag.
//   3. The post-merge L6 overlay must land on the dispatched bag (the
//      stub records every dispatch's Attributes verbatim, so the assertion
//      is "the worker dispatch saw the overlaid value").
//
// The supervisor's deep-merge happens in runtime/breakpoint_eval.go;
// the assertion here pins that the merge actually reached the executor
// rather than just the snapshot.
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

func TestResumeWithOverlay(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Stub mirrors the input bag back as a no-op; we only need the
	// Observed entry for the assertion. `ok` is the writeback slot the
	// stub fills; `tag` is the operator-visible field the overlay sets.
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

	hit := waitForHitOnBreakpoint(t, h, bpID, 10*time.Second)

	// Resume WITH overlay that injects `tag` into the bag. The bag is
	// schema-valid (additionalProperties is undefined → permissive),
	// so the validate step inside ValidateAndPersistResume passes.
	status, out := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id":  hit.ID.String(),
		"overlay": map[string]any{"tag": "overlay-value"},
	})
	require.Equal(t, http.StatusOK, status, "resume should succeed: %v", out)
	require.Equal(t, true, out["first_resume"])

	// Hit row should now carry the overlay verbatim — surfaceable to a
	// later operator inspecting the audit trail.
	row := getHitRow(t, h, hit.ID)
	require.NotNil(t, row.ResumeOverlay)
	require.Equal(t, "overlay-value", row.ResumeOverlay["tag"])

	// The dispatch should reach the executor with the overlay merged
	// into the attribute bag. The stub records the request verbatim.
	require.True(t, waitForStubObservedCount(h, "worker", 1, 10*time.Second),
		"stub should observe the worker dispatch after resume")
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
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh after the executor returns terminal/success")
}
