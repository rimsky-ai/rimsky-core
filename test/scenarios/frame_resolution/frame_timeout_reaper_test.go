// Verifies spec §7: the scheduler tick reaps a "stuck" frame — one
// that has exceeded its frame_timeout_ms with no live executor work
// (no claimed dispatches) but with at least one in-motion node
// (state stale/running). On reap, the frame transitions to failed
// and any wedged nodes are forced to failed too.
//
// Mechanism: insert the wedged state directly via SQL (a stale node
// with no dispatch row, frame started_at far enough in the past).
// frame.RunTick (driven by the scheduler tick) detects the stuck
// frame and reaps it.
package frame_resolution

import (
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/modeling/frame"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
)

func TestFrameTimeoutReaper(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name:            "timeout-reaper",
		Version:         "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		FrameTimeoutMs:  60000,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-timeout", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Drop any auto-created frame for this instance so we have full control.
	h.ExecSQL(`DELETE FROM rimsky_dispatch WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	h.ExecSQL(`UPDATE rimsky_nodes SET state = 'fresh', frame_id = NULL WHERE id = $1`, uuid.UUID(worker.ID))

	// Insert a wedged frame: started_at 2 minutes ago (past the 60s timeout),
	// state=running, no claimed dispatches, but the source node is stale
	// with the frame_id (no supervisor will pick it up because there's no
	// dispatch row tying it to a runnable queue entry — yet rimsky_nodes
	// is in_motion, so the frame's expected work is not done).
	var frameID uuid.UUID
	h.QueryRowSQL(`
		INSERT INTO rimsky_frames(instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now() - interval '3 minutes', now() - interval '2 minutes', 60000)
		RETURNING frame_id
	`, []any{uuid.UUID(iid), uuid.UUID(worker.ID)}, &frameID)

	// Mark the source node stale with this frame_id but no dispatch row.
	h.ExecSQL(`
		UPDATE rimsky_nodes SET state = 'stale', frame_id = $1, updated_at = now() WHERE id = $2
	`, frameID, uuid.UUID(worker.ID))

	// Drive the frame engine.
	require.NoError(t, frame.RunTick(h.Ctx, h.Driver.Store(), h.Driver.Queue(), slog.Default()))

	// Frame should now be failed.
	state, ok := waitForFrameTerminal(t, h, frameID, 5*time.Second)
	require.True(t, ok, "stuck frame did not transition to terminal")
	require.Equal(t, "failed", state, "stuck frame should be failed by reaper")

	// Wedged node should have been forced to failed.
	var nodeState string
	h.QueryRowSQL(
		`SELECT state FROM rimsky_nodes WHERE id = $1`,
		[]any{uuid.UUID(worker.ID)}, &nodeState)
	require.Equal(t, "failed", nodeState,
		"wedged node should be forced to failed by reaper")
}
