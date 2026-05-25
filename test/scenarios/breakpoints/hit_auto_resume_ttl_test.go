// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Hit auto-resume via TTL":
//
//   1. Install a pause-mode breakpoint with overflow_policy =
//      auto_resume_after_ttl and hit_ttl_seconds = 1.
//   2. The supervisor's dispatch hits the breakpoint and is parked
//      inside waitForResume.
//   3. No agent issues a resume call.
//   4. Within ~2s the scheduler's sweep tick (`AutoResumeStale`) stamps
//      `resumed_at = NOW(), resumed_by_key = 'sweeper'` on the hit row.
//   5. The blocked runner's poll returns the resumed row and the
//      dispatch proceeds (with no overlay) to terminal/success.
//
// Pins the §4.8 auto_resume_after_ttl contract: a parked dispatch is
// guaranteed to proceed within hit_ttl_seconds + one sweep tick, even
// if the agent never calls resume.
//
// @concept: breakpoint

package breakpoints

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestHitAutoResumeTTL(t *testing.T) {
	t.Parallel()
	// Use a fast scheduler tick so the AutoResumeStale sweep fires
	// inside the assertion window. The default 250ms tick suffices but
	// we explicitly request 100ms so the test is bounded tight.
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

	// Hit lands; we DO NOT issue a resume — the sweeper must do it.
	hit := waitForHitOnBreakpoint(t, h, bpID, 10*time.Second)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"executor must not be called while paused at the breakpoint")

	// Within hit_ttl_seconds (1s) + a sweep tick (~100ms) + the runner's
	// poll cadence (250ms), the dispatch should proceed. Give the loop
	// 10s of slack to absorb CI jitter.
	require.True(t, waitForStubObservedCount(h, "worker", 1, 10*time.Second),
		"executor should observe dispatch after the sweeper auto-resumes the stale hit")

	// Verify the row reflects sweeper-driven resume.
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
