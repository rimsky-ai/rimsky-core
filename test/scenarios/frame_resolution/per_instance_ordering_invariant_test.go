// Verifies blessed invariant 16 (spec §18): "Per-instance ordering — at
// most one running frame per instance. Enforced by uq_rimsky_frames_running."
//
// Two checks:
//  1. Direct SQL: insert two running rows for the same instance; second insert
//     must fail with a unique-violation.
//  2. Concurrent fires through frame.EnqueueOrCoalesce + RunTick advancement
//     do not produce more than one running frame at a time over the test's
//     observation window.
package frame_resolution

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestPerInstanceOrderingInvariant_DirectSQL(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-instance-ordering", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-ordering-direct", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// CreateInstance auto-enqueues a frame for the root. Clear it so the
	// test's own inserts have full control.
	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_dispatch WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx, `UPDATE rimsky_nodes SET state = 'fresh', frame_id = NULL WHERE id = $1`, uuid.UUID(worker.ID))
	require.NoError(t, err)

	// First running insert: should succeed.
	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
	`, uuid.UUID(iid), uuid.UUID(worker.ID))
	require.NoError(t, err, "first running insert should succeed")

	// Second running insert: must fail (uq_rimsky_frames_running).
	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
	`, uuid.UUID(iid), uuid.UUID(worker.ID))
	require.Error(t, err, "second running insert must fail")
	require.Contains(t, strings.ToLower(err.Error()), "uq_rimsky_frames_running",
		"expected unique-violation on uq_rimsky_frames_running; got %v", err)
}

func TestPerInstanceOrderingInvariant_Concurrent(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-instance-ordering-concurrent", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-ordering-conc", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Fire 10 invalidates concurrently.
	const N = 10
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			fireInvalidate(t, h, iid, worker.ID)
		}()
	}
	wg.Wait()

	// Poll the rimsky_frames table over a 5-second window asserting at most one
	// row in 'running' at any time.
	var maxRunning atomic.Int32
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n := countFramesByState(t, h.Pool, iid, "running")
		if int32(n) > maxRunning.Load() {
			maxRunning.Store(int32(n))
		}
		require.LessOrEqual(t, n, 1,
			"observed %d running frames simultaneously for instance %s", n, iid)
		time.Sleep(20 * time.Millisecond)
	}

	// And eventually all queued+running frames drain to terminal states.
	require.True(t,
		waitForFramesByState(t, h.Pool, iid, "completed", N+1, 30*time.Second) ||
			eventuallyAllTerminal(h, iid, 30*time.Second),
		"expected all frames to terminate eventually")
}

func eventuallyAllTerminal(h *scenario.Harness, iid shared.UUID, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n int
		_ = h.Pool.QueryRow(context.Background(), `
			SELECT count(*) FROM rimsky_frames
			WHERE instance_id = $1 AND state IN ('queued','running')
		`, uuid.UUID(iid)).Scan(&n)
		if n == 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
