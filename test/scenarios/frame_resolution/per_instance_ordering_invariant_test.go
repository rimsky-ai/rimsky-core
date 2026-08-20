// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package frame_resolution

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
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

	freshScope1 := createFreshRunScope(t, h, iid)
	freshScope2 := createFreshRunScope(t, h, iid)
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
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, started_at, root_run_scope_id)
		VALUES ($1, $2, now(), $3)
	`, uuid.UUID(iid), messageIDFirst, uuid.UUID(freshScope1))
	require.NoError(t, err, "first running insert should succeed")

	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, started_at, root_run_scope_id)
		VALUES ($1, $2, now(), $3)
	`, uuid.UUID(iid), messageIDSecond, uuid.UUID(freshScope2))
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

	waitFramesCompletedOrAllEnded(t, h, iid, N+1)

	var overlaps int
	h.QueryRowSQL(`
		SELECT count(*)
		  FROM rimsky_frames a
		  JOIN rimsky_frames b
		    ON b.instance_id = a.instance_id
		   AND b.frame_id <> a.frame_id
		   AND a.started_at IS NOT NULL
		   AND b.started_at IS NOT NULL
		   AND b.started_at < coalesce(a.ended_at, 'infinity'::timestamptz)
		   AND a.started_at < coalesce(b.ended_at, 'infinity'::timestamptz)
		 WHERE a.instance_id = $1
	`, []any{uuid.UUID(iid)}, &overlaps)
	require.Zero(t, overlaps,
		"two frames of instance %s overlapped in the durable frame record — per-instance ordering serializes frames, "+
			"so no frame may start before the previous one ended", iid)
}

func waitFramesCompletedOrAllEnded(t *testing.T, h *scenario.Harness, iid shared.UUID, wantCompleted int) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("%d completed frame(s) for instance %s, or every frame ended", wantCompleted, iid), func() bool {
		if countFramesByState(t, h, iid, "completed") == wantCompleted {
			return true
		}
		var n int
		_ = h.Pool.QueryRow(context.Background(), `
			SELECT count(*) FROM rimsky_frames
			WHERE instance_id = $1 AND ended_at IS NULL
		`, uuid.UUID(iid)).Scan(&n)
		return n == 0
	})
}
