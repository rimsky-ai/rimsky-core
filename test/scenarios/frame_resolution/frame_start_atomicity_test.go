// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies blessed invariant 18 (spec §18): "Frame-start atomicity —
// queued→running transition AND source-node state='stale', frame_id=$frame_id
// writes happen in one transaction."
//
// Mechanism: insert a queued frame; race two concurrent frame.RunTick
// goroutines. Exactly one CAS wins; the other rolls back. After the
// dust settles, the running frame's source nodes show state='stale'
// (or have already cascaded onward) with the frame_id set, atomically.
package frame_resolution

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/graph/frame"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

func TestFrameStartAtomicity(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "frame-start-atomicity", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-atomicity", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Reset to a clean queued frame state for full control. Post-
	// stage-3 cutover: state lives on rimsky_node_runs; clearing the
	// in-flight run rows + the node-row frame_id is the equivalent
	// reset.
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	h.ExecSQL(`UPDATE rimsky_nodes SET frame_id = NULL WHERE id = $1`, uuid.UUID(worker.ID))

	var frameID uuid.UUID
	h.QueryRowSQL(`
		INSERT INTO rimsky_frames(instance_id, frame_resolution_mode, state, source_node_ids, queued_at, frame_timeout_ms)
		VALUES ($1, 'serial_queue', 'queued', ARRAY[$2]::UUID[], now(), 600000)
		RETURNING frame_id
	`, []any{uuid.UUID(iid), uuid.UUID(worker.ID)}, &frameID)

	// Race two RunTicks.
	var wg sync.WaitGroup
	const N = 4
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(), slog.Default())
		}()
	}
	wg.Wait()

	// Exactly one frame should have advanced to running (per uq_rimsky_frames_running).
	require.Equal(t, 1, countFramesByState(t, h, iid, "running"),
		"exactly one frame should be running after the race")

	// Atomic visibility: running row has started_at set, AND the source node
	// has state='stale' (or has progressed onward via supervisor) with the matching frame_id.
	var state string
	var startedAt *time.Time
	h.QueryRowSQL(
		`SELECT state, started_at FROM rimsky_frames WHERE frame_id = $1`,
		[]any{frameID}, &state, &startedAt)
	require.Equal(t, "running", state)
	require.NotNil(t, startedAt, "running frame must have started_at set atomically")

	// Post-stage-3 cutover: state comes from the in-flight run row.
	var nodeState string
	var nodeFrameID *uuid.UUID
	h.QueryRowSQL(
		`SELECT COALESCE(r.state, 'fresh'), n.frame_id
		   FROM rimsky_nodes n
		   LEFT JOIN rimsky_node_runs r
		          ON r.node_id = n.id
		         AND r.phase IN ('pending','active','held','parked')
		  WHERE n.id = $1`,
		[]any{uuid.UUID(worker.ID)}, &nodeState, &nodeFrameID)
	require.NotNil(t, nodeFrameID,
		"source node must have frame_id set atomically with frame-start")
	require.Equal(t, frameID, *nodeFrameID,
		"source node frame_id must match the started frame")
	require.Contains(t, []string{"stale", "running"}, nodeState,
		"source node should be stale or running after frame-start; got %q", nodeState)
}
