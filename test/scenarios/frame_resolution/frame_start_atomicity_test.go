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
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
)

func TestFrameStartAtomicity(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "frame-start-atomicity", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-atomicity", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Reset to a clean queued frame state for full control.
	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_dispatch WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx, `UPDATE rimsky_nodes SET state = 'fresh', frame_id = NULL WHERE id = $1`, uuid.UUID(worker.ID))
	require.NoError(t, err)

	var frameID uuid.UUID
	err = h.Pool.QueryRow(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms)
		VALUES ($1, 'serial_queue', 'queued', ARRAY[$2]::UUID[], now(), 600000)
		RETURNING frame_id
	`, uuid.UUID(iid), uuid.UUID(worker.ID)).Scan(&frameID)
	require.NoError(t, err)

	// Race two RunTicks.
	var wg sync.WaitGroup
	const N = 4
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = frame.RunTick(h.Ctx, h.Pool, slog.Default())
		}()
	}
	wg.Wait()

	// Exactly one frame should have advanced to running (per uq_rimsky_frames_running).
	require.Equal(t, 1, countFramesByState(t, h.Pool, iid, "running"),
		"exactly one frame should be running after the race")

	// Atomic visibility: running row has started_at set, AND the source node
	// has state='stale' (or has progressed onward via supervisor) with the matching frame_id.
	var state string
	var startedAt *time.Time
	err = h.Pool.QueryRow(context.Background(),
		`SELECT state, started_at FROM rimsky_frames WHERE frame_id = $1`, frameID).Scan(&state, &startedAt)
	require.NoError(t, err)
	require.Equal(t, "running", state)
	require.NotNil(t, startedAt, "running frame must have started_at set atomically")

	var nodeState string
	var nodeFrameID *uuid.UUID
	err = h.Pool.QueryRow(context.Background(),
		`SELECT state, frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&nodeState, &nodeFrameID)
	require.NoError(t, err)
	require.NotNil(t, nodeFrameID,
		"source node must have frame_id set atomically with frame-start")
	require.Equal(t, frameID, *nodeFrameID,
		"source node frame_id must match the started frame")
	require.Contains(t, []string{"stale", "running"}, nodeState,
		"source node should be stale or running after frame-start; got %q", nodeState)
}
