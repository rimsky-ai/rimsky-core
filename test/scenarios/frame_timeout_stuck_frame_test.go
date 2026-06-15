// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 41 — frame_timeout_stuck_frame.
//
// Per spec §7, a stuck frame (no progress for longer than
// frame_timeout_ms with at least one in-motion node and no claimed
// dispatches) MUST trip the observer. The observer logs a single
// `frame.stuck.observed` warning and takes no destructive action: the
// frame stays running, no nodes are failed.
//
// Mechanism: seed a wedged frame with last_progress_at far in the past;
// run frame.RunTick with a logger that captures Warn calls; verify
// (a) the warning is logged, (b) the frame state stays running,
// (c) the wedged node keeps its state.
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

	// @deliberate: Drop any auto-created frames so we have full control. Post-
	// stage-3 cutover: state lives on rimsky_node_runs.
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	h.ExecSQL(`UPDATE rimsky_nodes SET frame_id = NULL WHERE id = $1`, uuid.UUID(worker.ID))

	// @deliberate: Seed a wedged frame with last_progress_at 5 minutes in the past
	// against a 60s timeout; the source node is stale within this frame
	// and there are no claimed dispatches. Pass 1 of the message-schema-
	// layer plan added the rimsky_frames.triggering_message_id NOT NULL
	// FK; seed a typed envelope first so the frame's FK resolves.
	const timeoutMs = 60000
	messageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/frame-stuck', 'operator', 'operator')`,
		messageID, uuid.UUID(iid))
	var frameID uuid.UUID
	h.QueryRowSQL(`
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, state, queued_at, started_at, last_progress_at, frame_timeout_ms)
		VALUES ($1, $2, 'running', now() - interval '10 minutes', now() - interval '5 minutes', now() - interval '5 minutes', $3)
		RETURNING frame_id
	`, []any{uuid.UUID(iid), messageID, int64(timeoutMs)}, &frameID)
	h.ExecSQL(`UPDATE rimsky_nodes SET frame_id = $1, updated_at = now() WHERE id = $2`,
		frameID, uuid.UUID(worker.ID))
	mainScopeID := h.GetMainRunScopeID(iid)
	h.ExecSQL(`
		INSERT INTO rimsky_node_runs
		    (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		VALUES (gen_random_uuid(), $1, 'stub', ARRAY[]::text[], now(), 'pending', 'stale', $2, $3)
	`, uuid.UUID(worker.ID), frameID, uuid.UUID(mainScopeID))

	// @deliberate: Capture log output via a buffer-backed slog handler.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// @deliberate: Drive the frame engine.
	require.NoError(t, frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(), logger))

	logged := buf.String()
	require.Contains(t, logged, "frame.stuck.observed",
		"expected stuck-frame warning; got logger output: %q", logged)

	// @deliberate: The frame must stay running — the warning is non-destructive.
	var state string
	h.QueryRowSQL(`SELECT state FROM rimsky_frames WHERE frame_id = $1`,
		[]any{frameID}, &state)
	require.Equal(t, "running", state,
		"stuck-frame warning must not transition the frame to terminal")

	// @deliberate: The wedged source node must keep its state — no fail-fanout.
	// Post-stage-3: read state from the in-flight run row.
	var nodeState string
	h.QueryRowSQL(`SELECT COALESCE(r.state, 'fresh')
	                 FROM rimsky_nodes n
	                 LEFT JOIN rimsky_node_runs r
	                        ON r.node_id = n.id
	                       AND r.phase IN ('pending','active','held','parked')
	                WHERE n.id = $1`,
		[]any{uuid.UUID(worker.ID)}, &nodeState)
	require.Equal(t, "stale", nodeState,
		"warning must not mutate node state")

	require.True(t, strings.Contains(logged, frameID.String()),
		"warning should mention frame_id %s; got %q", frameID.String(), logged)
}
