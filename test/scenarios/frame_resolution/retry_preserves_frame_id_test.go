// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies that an applyErrorPolicy → retry path that transitions
// a node back to 'stale' preserves frame_id. Frame-end detection
// (runFrameEndDetection) filters by `n.frame_id = f.frame_id`, so a
// retried-but-still-in-the-same-frame node must continue to count as
// in-flight under the running frame's predicate.
//
// This test catches review Issue 8: if a transition back to stale
// inadvertently cleared frame_id, frame-end could fire prematurely
// while the retry was in flight.
package frame_resolution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

// TestRetryDoesNotPrematurelyEndFrame is a targeted check: while the
// node is in the stale-after-retry-release state but before the
// re-enqueued dispatch claims, the frame-end predicate must NOT fire
// (the node still has frame_id pointing at the running frame).
//
// We drive this directly via SQL to model the supervisor's retry path
// behaviour without depending on stub-script support for retry
// sequences.
func TestRetryDoesNotPrematurelyEndFrame(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = ctx

	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-frame-end-predicate", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-pred", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wipe any auto-enqueued initial frame + in-flight rows for this
	// instance so the test starts clean. Post-stage-3 cutover: state
	// lives on rimsky_node_runs.
	_, err := h.Pool.Exec(h.Ctx,
		`DELETE FROM rimsky_node_runs WHERE node_id = $1`, uuid.UUID(worker.ID))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx,
		`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes SET frame_id=NULL WHERE id=$1`,
		uuid.UUID(worker.ID))
	require.NoError(t, err)

	// Manually create a running frame; mark the worker stale via an
	// in-flight pending run row pinned to the frame (simulating what
	// advanceOneFrame does at frame-start).
	var frameID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, frame_resolution_mode, state, source_node_ids,
			queued_at, started_at, frame_timeout_ms)
		VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
		RETURNING frame_id
	`, uuid.UUID(iid), uuid.UUID(worker.ID)).Scan(&frameID))
	_, err = h.Pool.Exec(h.Ctx, `UPDATE rimsky_nodes SET frame_id=$1 WHERE id=$2`,
		frameID, uuid.UUID(worker.ID))
	require.NoError(t, err)
	// Insert the in-flight active running run row (state machine reads
	// current state from here for the retry simulation below).
	mainScopeID := h.GetMainRunScopeID(iid)
	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_node_runs
		    (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		VALUES (gen_random_uuid(), $1, 'stub', ARRAY[]::text[], now(), 'active', 'running', $2, $3)
	`, uuid.UUID(worker.ID), frameID, uuid.UUID(mainScopeID))
	require.NoError(t, err)

	// Simulate the runner's retry path: UpdateState(running → stale,
	// ReasonPolicyRetry) must preserve the node-row frame_id.
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		return h.Persist.Nodes().UpdateState(h.Ctx,
			worker.ID, h.GetMainRunScopeID(iid), "stale", cascade.ReasonPolicyRetry, nil, tx)
	}))

	// Re-read frame_id; it must still be set.
	var preservedFrameID *uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&preservedFrameID))
	require.NotNil(t, preservedFrameID, "retry must preserve frame_id on the stale node")
	require.Equal(t, frameID, *preservedFrameID)

	// The frame-end predicate counts in-flight run rows in state
	// IN ('stale','running') for the frame. With the retried node in
	// stale + matching frame_id, the predicate must not fire.
	var inflight int
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		SELECT count(*) FROM rimsky_node_runs r
		JOIN rimsky_frames f ON f.frame_id = r.frame_id
		WHERE f.state = 'running'
		  AND r.phase IN ('pending','active','held','parked')
		  AND r.state IN ('stale','running')
	`).Scan(&inflight))
	require.Equal(t, 1, inflight,
		"retried-stale run row must still register as in-flight under the running frame")
}
