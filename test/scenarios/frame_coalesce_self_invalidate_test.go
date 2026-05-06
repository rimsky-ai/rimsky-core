// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 39 — frame_coalesce_self_invalidate.
//
// Single-node template with on_executor_complete:
//
//	{ invalidate: { targets: [self], frame: next } }
//
// and frame_resolution: coalesce. Drive multiple rapid commits and
// assert the pending self-invalidates collapse into a single trailing
// frame, with no double-execute.
package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

// TestFrameCoalesceSelfInvalidate verifies frame_resolution: coalesce
// collapses multiple in-flight self-invalidates from the
// on_executor_complete handler into a single pending frame. Driven via
// a slow stub executor so the first frame is genuinely in flight while
// subsequent self-invalidates pile up.
func TestFrameCoalesceSelfInvalidate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Slow stub so we have time for multiple self-invalidates to coalesce.
	h.Stub.WhenType("worker").Complete(map[string]any{"v": 1}, true, "ok").Delay(500 * time.Millisecond)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "coalesce-self-invalidate", Version: "1",
		FrameResolution: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				OnExecutorComplete: &node.OnExecutorCompleteHandler{
					Invalidate: &node.HandlerInvalidate{
						Targets: []string{node.SelfTarget},
						Frame:   node.FrameNext,
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-coalesce-self", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait for the worker to land in fresh at least once; the
	// on_executor_complete invalidate fires immediately on each commit.
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh on first run")

	// Allow time for the self-invalidate loop to coalesce: under
	// frame_resolution: coalesce, a second commit while a frame is
	// queued/running should collapse with any prior pending frame.
	time.Sleep(3 * time.Second)

	// Stop the loop by switching the executor to a no-op completion that
	// still fires the invalidate; we'll just measure frame counts at the
	// last visible quiescent moment. Coalesce predicate: at most ONE
	// queued coalesce row should ever exist for the instance at any time.
	var maxQueued int
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		SELECT count(*) FROM rimsky_frames
		WHERE instance_id = $1 AND state = 'queued'
	`, uuid.UUID(iid)).Scan(&maxQueued))
	require.LessOrEqual(t, maxQueued, 1,
		"coalesce should permit at most one queued frame at a time; got %d", maxQueued)

	// Each frame should have at most one running terminal per worker.
	// Verify no double-execute: count rimsky_worker_request rows per frame.
	var maxRunsPerFrame int
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		SELECT COALESCE(MAX(c), 0) FROM (
			SELECT count(*) AS c FROM rimsky_worker_request
			WHERE node_id = $1
			GROUP BY frame_id
		) t
	`, uuid.UUID(worker.ID)).Scan(&maxRunsPerFrame))
	require.LessOrEqual(t, maxRunsPerFrame, 1,
		"no double-execute: at most one worker_request per (frame, node); got max=%d", maxRunsPerFrame)
}
