// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies the frame-timeout observer is purely advisory: when a
// frame has exceeded its `frame_timeout_ms` with no live executor
// work (no claimed dispatches) but with at least one in-motion node
// (state stale/running), the scheduler tick emits a
// `frame.stuck.observed` slog warning and takes no destructive action.
// The frame stays running; wedged nodes keep their state; the
// instance is not terminated.
//
// Mechanism: insert the wedged state directly via SQL (a stale node
// with no dispatch row, frame `last_progress_at` far enough in the
// past). frame.RunTick (driven by the scheduler tick) detects the
// stuck frame and warns.
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
	// @deliberate: NoSupervisor: true — this test pre-arranges the wedged frame
	// state via direct DELETEs against rimsky_node_runs /
	// rimsky_claim_handles / rimsky_frames and then drives
	// frame.RunTick manually. A live supervisor poll-loop holds row
	// locks on those tables (via SelectCandidates / acquisition tx /
	// the new RefreshProgress UPDATE inside enforceAndUpdate) that
	// race with the test's seed DELETEs, surfacing as postgres
	// deadlock flakes. The supervisor isn't needed here — RunTick is
	// invoked explicitly — so we skip starting it.
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

	// @deliberate: Drop any auto-created frame for this instance so we have full control.
	// Post-stage-3 cutover: state lives on rimsky_node_runs; deleting
	// the in-flight run rows + clearing the node-row frame_id resets.
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	h.ExecSQL(`UPDATE rimsky_nodes SET frame_id = NULL WHERE id = $1`, uuid.UUID(worker.ID))

	// @deliberate: Insert a wedged frame: last_progress_at 2 minutes ago (past the 60s
	// timeout), state=running, no claimed dispatches, but the source node
	// is stale with the frame_id.
	//
	// Per the reactive-loops + lifecycle-handlers spec §7, frame_timeout_ms
	// is now compared against last_progress_at (not started_at).
	// Pass 1 of the message-schema-layer plan added the
	// rimsky_frames.triggering_message_id NOT NULL FK; seed a typed
	// envelope first so the frame's FK resolves.
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

	// @deliberate: Mark the source node stale by binding rimsky_nodes.frame_id + an
	// in-flight pending stale run row (post-stage-3: state lives on
	// the run row).
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
		"expected stuck-frame observation; got logger output: %q", logged)
	require.True(t, strings.Contains(logged, frameID.String()),
		"warning should mention frame_id %s; got %q", frameID.String(), logged)

	// @deliberate: Frame stays running — observation is non-destructive.
	var state string
	h.QueryRowSQL(`SELECT state FROM rimsky_frames WHERE frame_id = $1`,
		[]any{frameID}, &state)
	require.Equal(t, "running", state,
		"stuck-frame warning must not transition the frame to terminal")

	// @deliberate: Wedged node keeps its state (post-stage-3: read from the in-flight run row).
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

	// @deliberate: Instance terminated_at must be NULL — warning never terminates.
	var terminatedAt *time.Time
	h.QueryRowSQL(
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`,
		[]any{uuid.UUID(iid)}, &terminatedAt)
	require.Nil(t, terminatedAt,
		"warning must not set rimsky_instances.terminated_at")
}
