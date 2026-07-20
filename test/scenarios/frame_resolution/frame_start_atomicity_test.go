// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_resolution

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFrameStartAtomicity(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "frame-start-atomicity", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-atomicity", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	mainScopeID := h.GetMainRunScopeID(iid)
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))

	messageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/frame-start-atomicity', 'operator', 'operator')`,
		messageID, uuid.UUID(iid))
	var frameID uuid.UUID
	h.QueryRowSQL(`
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, started_at, root_run_scope_id)
		VALUES ($1, $2, now(), $3)
		RETURNING frame_id
	`, []any{uuid.UUID(iid), messageID, uuid.UUID(mainScopeID)}, &frameID)
	h.ExecSQL(`
		INSERT INTO rimsky_node_runs
		    (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, frame_id, run_scope_id)
		VALUES (gen_random_uuid(), $1, 'stub', ARRAY[]::text[], now(), 'stale', 1, $2, $3)
	`, uuid.UUID(worker.ID), frameID, uuid.UUID(mainScopeID))

	var wg sync.WaitGroup
	const N = 4
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(), slog.Default(), nil)
		}()
	}
	wg.Wait()

	require.Equal(t, 1, countFramesByState(t, h, iid, "running"),
		"exactly one frame should be running after the race")

	var state string
	var startedAt *time.Time
	h.QueryRowSQL(
		`SELECT CASE WHEN f.ended_at IS NULL THEN 'running' WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed' ELSE 'completed' END, f.started_at FROM rimsky_frames f WHERE frame_id = $1`,
		[]any{frameID}, &state, &startedAt)
	require.Equal(t, "running", state)
	require.NotNil(t, startedAt, "running frame must have started_at set atomically")

	var nodeState string
	var runFrameID *uuid.UUID
	h.QueryRowSQL(
		`SELECT COALESCE(r.state, 'fresh'), r.frame_id
		   FROM rimsky_nodes n
		   LEFT JOIN rimsky_node_runs r
		          ON r.node_id = n.id
		         AND r.state IN ('pending','stale','running','held','parked')
		  WHERE n.id = $1`,
		[]any{uuid.UUID(worker.ID)}, &nodeState, &runFrameID)
	require.NotNil(t, runFrameID,
		"in-flight run must carry the running frame's frame_id after frame-start")
	require.Equal(t, frameID, *runFrameID,
		"in-flight run frame_id must match the started frame")
	require.Contains(t, []string{"stale", "running"}, nodeState,
		"source node should be stale or running after frame-start; got %q", nodeState)
}
