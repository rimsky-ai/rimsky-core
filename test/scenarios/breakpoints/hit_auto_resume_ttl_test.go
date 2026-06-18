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

func TestHitAutoResumeTTL(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		SchedulerTick: 100 * time.Millisecond,
	})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ttl-resumed")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-hit-auto-resume-ttl", Version: "1",
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

	iid := createInstanceWithPause(t, h, tid, "ck-hit-auto-resume-ttl", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint":      "before_dispatch",
		"matcher":         map[string]any{"node_type": "worker"},
		"mode":            "pause",
		"overflow_policy": "auto_resume_after_ttl",
		"hit_ttl_seconds": 1,
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	hit := waitForHitOnBreakpoint(t, h, bpID, 10*time.Second)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not be called while paused at the breakpoint")

	require.True(t, waitForStubObservedCount(h, "worker", 1, 10*time.Second),
		"executor should observe dispatch after the sweeper auto-resumes the stale hit")

	row := getHitRow(t, h, hit.ID)
	require.NotNil(t, row)
	require.NotNil(t, row.ResumedAt,
		"sweeper must stamp resumed_at on the stale hit")
	require.NotNil(t, row.ResumedByKey)
	require.Equal(t, "sweeper", *row.ResumedByKey,
		"resumed_by_key must equal 'sweeper' for TTL auto-resumes")
	require.Nil(t, row.ResumeOverlay,
		"auto-resume carries no overlay (the dispatch proceeds with the original bag)")

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh after the auto-resume + executor terminal")
}
