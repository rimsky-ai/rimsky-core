// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_resolution

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFrameTimeoutWarning(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name:           "timeout-warning",
		Version:        "1",
		FrameTimeoutMs: 60000,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-timeout", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	h.ExecSQL(`UPDATE rimsky_nodes SET frame_id = NULL WHERE id = $1`, uuid.UUID(worker.ID))

	messageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/frame-timeout-warning', 'operator', 'operator')`,
		messageID, uuid.UUID(iid))
	var frameID uuid.UUID
	h.QueryRowSQL(`
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, state, queued_at, started_at, last_progress_at, frame_timeout_ms)
		VALUES ($1, $2, 'running', now() - interval '3 minutes', now() - interval '2 minutes', now() - interval '2 minutes', 60000)
		RETURNING frame_id
	`, []any{uuid.UUID(iid), messageID}, &frameID)

	h.ExecSQL(`UPDATE rimsky_nodes SET frame_id = $1, updated_at = now() WHERE id = $2`,
		frameID, uuid.UUID(worker.ID))
	mainScopeID := h.GetMainRunScopeID(iid)
	h.ExecSQL(`
		INSERT INTO rimsky_node_runs
		    (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		VALUES (gen_random_uuid(), $1, 'stub', ARRAY[]::text[], now(), 'pending', 'stale', $2, $3)
	`, uuid.UUID(worker.ID), frameID, uuid.UUID(mainScopeID))

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	require.NoError(t, frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(), logger))

	logged := buf.String()
	require.Contains(t, logged, "frame.stuck.observed",
		"expected stuck-frame observation; got logger output: %q", logged)
	require.True(t, strings.Contains(logged, frameID.String()),
		"warning should mention frame_id %s; got %q", frameID.String(), logged)

	var state string
	h.QueryRowSQL(`SELECT state FROM rimsky_frames WHERE frame_id = $1`,
		[]any{frameID}, &state)
	require.Equal(t, "running", state,
		"stuck-frame warning must not transition the frame to terminal")

	var nodeState string
	h.QueryRowSQL(
		`SELECT COALESCE(r.state, 'fresh')
		   FROM rimsky_nodes n
		   LEFT JOIN rimsky_node_runs r
		          ON r.node_id = n.id
		         AND r.phase IN ('pending','active','held','parked')
		  WHERE n.id = $1`,
		[]any{uuid.UUID(worker.ID)}, &nodeState)
	require.Equal(t, "stale", nodeState,
		"warning must not mutate node state")

	var terminatedAt *time.Time
	h.QueryRowSQL(
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`,
		[]any{uuid.UUID(iid)}, &terminatedAt)
	require.Nil(t, terminatedAt,
		"warning must not set rimsky_instances.terminated_at")
}
