// Verifies blessed invariant 19 (spec §18): "frame_id flows with
// cascade. No rimsky_worker_request row has frame_id IS NULL. No rimsky_nodes
// row in state stale or running has frame_id IS NULL."
//
// Runs a multi-node cascade (source → middle → leaf) to a mid-flight
// state, asserts the invariant holds across the lifecycle, and asserts
// that completed nodes have frame_id cleared while failed nodes
// preserve it.
package frame_resolution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestNoNullFrameIDOnInFlightDispatch(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")
	h.Stub.WhenType("middle").Complete(map[string]any{}, true, "ok")
	h.Stub.WhenType("leaf").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "no-null-frame-id", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "middle", Executor: "stub", Dependencies: []string{"worker"}}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "leaf", Executor: "stub", Dependencies: []string{"middle"}}),
		},
	})
	iid := h.CreateInstance(tid, "ck-no-null", map[string]any{})

	src := h.FindNode(iid, "worker")
	require.NotNil(t, src)
	leaf := h.FindNode(iid, "leaf")
	require.NotNil(t, leaf)

	// Wait for the cascade to reach the leaf.
	require.True(t, h.WaitForNodeState(leaf.ID, shared.NodeStateFresh, 15*time.Second),
		"leaf did not reach fresh")

	// Invariant: no NULL frame_id on any rimsky_worker_request row anywhere.
	var nullDispatches int
	err := h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM rimsky_worker_request WHERE frame_id IS NULL`).Scan(&nullDispatches)
	require.NoError(t, err)
	require.Equal(t, 0, nullDispatches,
		"invariant 19 violated: %d rimsky_worker_request rows have NULL frame_id", nullDispatches)

	// Invariant: no non-fresh rimsky_nodes row with NULL frame_id.
	var nullNodes int
	err = h.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM rimsky_nodes
		WHERE state IN ('stale','running') AND frame_id IS NULL
	`).Scan(&nullNodes)
	require.NoError(t, err)
	require.Equal(t, 0, nullNodes,
		"invariant 19 violated: %d non-fresh rimsky_nodes rows have NULL frame_id", nullNodes)

	// On completion: nodes return to fresh and frame_id is cleared
	// (per spec §6.2 — completed clears frame_id, failed preserves it).
	for _, nodeType := range []string{"worker", "middle", "leaf"} {
		nID := h.FindNode(iid, nodeType).ID
		var state string
		var frameID *uuid.UUID
		err := h.Pool.QueryRow(context.Background(),
			`SELECT state, frame_id FROM rimsky_nodes WHERE id = $1`,
			uuid.UUID(nID)).Scan(&state, &frameID)
		require.NoError(t, err)
		if state == string(shared.NodeStateFresh) {
			require.Nil(t, frameID,
				"node %s in fresh state should have frame_id = NULL; got %v", nodeType, frameID)
		}
	}

	// Dispatch rows: every one (terminal or otherwise) carries a non-NULL frame_id.
	rows, err := h.Pool.Query(context.Background(),
		`SELECT id, frame_id FROM rimsky_worker_request`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var dispatchID, frameID uuid.UUID
		require.NoError(t, rows.Scan(&dispatchID, &frameID))
		require.NotEqual(t, uuid.Nil, frameID,
			"dispatch %s has zero frame_id", dispatchID)
	}
	require.NoError(t, rows.Err())
}
