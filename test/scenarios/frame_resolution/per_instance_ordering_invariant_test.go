// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestPerInstanceOrderingInvariant_DirectSQL(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-instance-ordering", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-ordering-direct", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	mainScopeID := h.GetMainRunScopeID(iid)
	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	require.NoError(t, err)

	messageIDFirst := uuid.New()
	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		VALUES ($1, $2, 'fixture/per-instance-ordering-1', 'operator', 'operator')
	`, messageIDFirst, uuid.UUID(iid))
	require.NoError(t, err)
	messageIDSecond := uuid.New()
	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		VALUES ($1, $2, 'fixture/per-instance-ordering-2', 'operator', 'operator')
	`, messageIDSecond, uuid.UUID(iid))
	require.NoError(t, err)

	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, started_at, frame_timeout_ms, root_run_scope_id)
		VALUES ($1, $2, now(), 600000, $3)
	`, uuid.UUID(iid), messageIDFirst, uuid.UUID(mainScopeID))
	require.NoError(t, err, "first running insert should succeed")

	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, started_at, frame_timeout_ms, root_run_scope_id)
		VALUES ($1, $2, now(), 600000, $3)
	`, uuid.UUID(iid), messageIDSecond, uuid.UUID(mainScopeID))
	require.Error(t, err, "second running insert must fail")
	require.Contains(t, strings.ToLower(err.Error()), "uq_rimsky_frames_open",
		"expected unique-violation on uq_rimsky_frames_open; got %v", err)
}

func TestPerInstanceOrderingInvariant_Concurrent(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-instance-ordering-concurrent", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-ordering-conc", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	const N = 10
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			postInvalidateMessage(t, h, iid)
		}()
	}
	wg.Wait()

	var maxRunning atomic.Int32
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n := countFramesByState(t, h, iid, "running")
		if int32(n) > maxRunning.Load() {
			maxRunning.Store(int32(n))
		}
		require.LessOrEqual(t, n, 1,
			"observed %d running frames simultaneously for instance %s", n, iid)
		time.Sleep(20 * time.Millisecond)
	}

	waitFramesCompletedOrAllEnded(t, h, iid, N+1)
}

func waitFramesCompletedOrAllEnded(t *testing.T, h *scenario.Harness, iid shared.UUID, wantCompleted int) {
	t.Helper()
	for {
		if countFramesByState(t, h, iid, "completed") == wantCompleted {
			return
		}
		var n int
		_ = h.Pool.QueryRow(context.Background(), `
			SELECT count(*) FROM rimsky_frames
			WHERE instance_id = $1 AND ended_at IS NULL
		`, uuid.UUID(iid)).Scan(&n)
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
