// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFrameTimeoutStuckFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "frame-timeout-stuck", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-frame-stuck", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	mainScopeID := h.GetMainRunScopeID(iid)
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))

	const timeoutMs = 60000
	messageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/frame-stuck', 'operator', 'operator')`,
		messageID, uuid.UUID(iid))
	var frameID uuid.UUID
	h.QueryRowSQL(`
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, started_at, last_progress_at, frame_timeout_ms, root_run_scope_id)
		VALUES ($1, $2, now() - interval '10 minutes', now() - interval '5 minutes', $3, $4)
		RETURNING frame_id
	`, []any{uuid.UUID(iid), messageID, int64(timeoutMs), uuid.UUID(mainScopeID)}, &frameID)
	h.ExecSQL(`
		INSERT INTO rimsky_node_runs
		    (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
		VALUES (gen_random_uuid(), $1, 'stub', ARRAY[]::text[], now(), 'stale', 'cascade', 1, $2, $3)
	`, uuid.UUID(worker.ID), frameID, uuid.UUID(mainScopeID))

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	require.NoError(t, frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(), logger))

	logged := buf.String()
	require.Contains(t, logged, "frame.stuck.observed",
		"expected stuck-frame warning; got logger output: %q", logged)

	var state string
	h.QueryRowSQL(`SELECT CASE WHEN f.ended_at IS NULL THEN 'running' WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed' ELSE 'completed' END FROM rimsky_frames f WHERE frame_id = $1`,
		[]any{frameID}, &state)
	require.Equal(t, "running", state,
		"stuck-frame warning must not transition the frame to terminal")

	var nodeState string
	h.QueryRowSQL(`SELECT COALESCE(r.state, 'fresh')
	                 FROM rimsky_nodes n
	                 LEFT JOIN rimsky_node_runs r
	                        ON r.node_id = n.id
	                       AND r.state IN ('pending','stale','running','held','parked')
	                WHERE n.id = $1`,
		[]any{uuid.UUID(worker.ID)}, &nodeState)
	require.Equal(t, "stale", nodeState,
		"warning must not mutate node state")

	require.True(t, strings.Contains(logged, frameID.String()),
		"warning should mention frame_id %s; got %q", frameID.String(), logged)
}
