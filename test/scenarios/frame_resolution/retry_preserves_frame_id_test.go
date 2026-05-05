// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies that an applyTerminalAppError → retry path that transitions
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
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
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
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-pred", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wipe any auto-enqueued initial frame for this instance so the test
	// starts from a clean state.
	_, err := h.Pool.Exec(h.Ctx,
		`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes SET state='fresh', frame_id=NULL WHERE id=$1`,
		uuid.UUID(worker.ID))
	require.NoError(t, err)

	// Manually create a running frame; mark the worker stale +
	// frame_id (simulating what advanceOneFrame does at frame-start).
	var frameID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, mode, state, source_node_ids,
			queued_at, started_at, frame_timeout_ms)
		VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
		RETURNING frame_id
	`, uuid.UUID(iid), uuid.UUID(worker.ID)).Scan(&frameID))
	_, err = h.Pool.Exec(h.Ctx, `
		UPDATE rimsky_nodes SET state='stale', frame_id=$1 WHERE id=$2
	`, frameID, uuid.UUID(worker.ID))
	require.NoError(t, err)

	// Simulate the runner's retry path: the supervisor flipped the node
	// from running → stale via ReasonPolicyRetry. The state machine's
	// running → stale is allowed under that reason; in production the
	// transition happens via UpdateState, which now also clears frame_id
	// only when the target is 'fresh' (the defensive guard added for
	// review Issue 24). For 'stale', frame_id must be preserved.
	//
	// We directly assert the property at the storage layer: an
	// UpdateState(running→stale, ReasonPolicyRetry) must NOT null
	// frame_id. To exercise that, set state='running' first, then call
	// UpdateState.
	_, err = h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes SET state='running' WHERE id=$1`,
		uuid.UUID(worker.ID))
	require.NoError(t, err)
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		return h.Persist.Nodes().UpdateState(h.Ctx,
			worker.ID, "stale", cascade.ReasonPolicyRetry, tx)
	}))

	// Re-read frame_id; it must still be set.
	var preservedFrameID *uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&preservedFrameID))
	require.NotNil(t, preservedFrameID, "retry must preserve frame_id on the stale node")
	require.Equal(t, frameID, *preservedFrameID)

	// The frame-end predicate counts in-flight nodes by `n.frame_id =
	// f.frame_id AND n.state IN ('stale','running')`. With the retried
	// node in stale + matching frame_id, the predicate must not fire.
	var inflight int
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		SELECT count(*) FROM rimsky_nodes n
		JOIN rimsky_frames f ON f.frame_id = n.frame_id
		WHERE f.state = 'running'
		  AND n.state IN ('stale','running')
		  AND n.frame_id = f.frame_id
	`).Scan(&inflight))
	require.Equal(t, 1, inflight,
		"retried-stale node must still register as in-flight under the running frame")
}
