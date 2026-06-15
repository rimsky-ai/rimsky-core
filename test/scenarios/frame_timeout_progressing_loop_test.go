// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 40 — frame_timeout_progressing_loop.
//
// Per spec §7, frame_timeout_ms now measures "no progress in window."
// A progressing self-invalidate loop where each iteration advances the
// frame's last_progress_at must NOT trip the soft-warning observer,
// even if total runtime exceeds the timeout window.
//
// Mechanism: seed a running frame with the minimum-allowed timeout
// (60s, the schema floor) but with last_progress_at refreshed each
// iteration. Then refresh last_progress_at (as the supervisor's
// persistence write path does on every node-state transition) and
// confirm the observer does not fire. Finally, stop refreshing and
// confirm the observer does fire — proving the test apparatus
// actually exercises the predicate. The frame state stays running
// throughout: the observer is purely advisory.
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

func TestFrameTimeoutProgressingLoop(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "frame-timeout-progressing", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-frame-prog", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// @deliberate: Drop any auto-created frames so we have full control. Post-
	// stage-3 cutover: state lives on rimsky_node_runs.
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	h.ExecSQL(`UPDATE rimsky_nodes SET frame_id = NULL WHERE id = $1`, uuid.UUID(worker.ID))

	// @deliberate: Seed a running frame with timeout = 60000ms (schema floor). The
	// node is stale within the frame; no claimed dispatches. The
	// rimsky_frames.triggering_message_id NOT NULL FK requires a typed
	// envelope to exist first so the frame's FK resolves.
	const timeoutMs = 60000
	messageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/frame-progressing-loop', 'operator', 'operator')`,
		messageID, uuid.UUID(iid))
	var frameID uuid.UUID
	h.QueryRowSQL(`
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, state, queued_at, started_at, last_progress_at, frame_timeout_ms)
		VALUES ($1, $2, 'running', now() - interval '5 minutes', now() - interval '5 minutes', now(), $3)
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

	// @deliberate: Drive 5 progress refreshes simulating a self-invalidate loop. Each
	// iteration sets last_progress_at to NOW() — modeling the supervisor's
	// node-state-transition write path.
	var progressBuf bytes.Buffer
	progressLogger := slog.New(slog.NewTextHandler(&progressBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	for i := 0; i < 5; i++ {
		h.ExecSQL(`UPDATE rimsky_frames SET last_progress_at = NOW() WHERE frame_id = $1`, frameID)
		require.NoError(t, frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(), progressLogger))
		var state string
		h.QueryRowSQL(`SELECT state FROM rimsky_frames WHERE frame_id = $1`,
			[]any{frameID}, &state)
		require.Equal(t, "running", state,
			"progressing frame stays running — iteration %d", i)
	}
	require.False(t, strings.Contains(progressBuf.String(), "frame.stuck.observed"),
		"progressing frame must NOT trip the stuck-frame warning; got logger output: %q",
		progressBuf.String())

	// @deliberate: Now stop refreshing — back-date last_progress_at past the timeout
	// window — and confirm the observer now fires. Sanity check that the
	// test apparatus actually exercises the predicate. The frame state
	// stays running because the observer is non-destructive.
	var stuckBuf bytes.Buffer
	stuckLogger := slog.New(slog.NewTextHandler(&stuckBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h.ExecSQL(`UPDATE rimsky_frames SET last_progress_at = NOW() - interval '5 minutes' WHERE frame_id = $1`, frameID)
	require.NoError(t, frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(), stuckLogger))
	require.Contains(t, stuckBuf.String(), "frame.stuck.observed",
		"once last_progress_at falls outside the timeout window, the observer must warn; got logger output: %q",
		stuckBuf.String())
	var finalState string
	h.QueryRowSQL(`SELECT state FROM rimsky_frames WHERE frame_id = $1`,
		[]any{frameID}, &finalState)
	require.Equal(t, "running", finalState,
		"observer is non-destructive; frame must stay running even after the warning fires")
}
