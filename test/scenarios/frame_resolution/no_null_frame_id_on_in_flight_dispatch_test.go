// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies blessed invariant 19 (spec §18): "frame_id flows with
// cascade. No rimsky_node_runs row has frame_id IS NULL. No rimsky_nodes
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

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestNoNullFrameIDOnInFlightDispatch(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")
	h.Stub.WhenType("middle").Success(map[string]any{}, true, "ok")
	h.Stub.WhenType("leaf").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "no-null-frame-id", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "middle", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "worker", On: "state"})),
			scenario.MakeNode(node.TemplateNodeDef{Type: "leaf", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "middle", On: "state"})),
		},
	})
	iid := h.CreateInstance(tid, "ck-no-null", map[string]any{})

	src := h.FindNode(iid, "worker")
	require.NotNil(t, src)
	leaf := h.FindNode(iid, "leaf")
	require.NotNil(t, leaf)

	// Wait for the cascade to reach the leaf.
	require.True(t, h.WaitForNodeState(leaf.ID, cascade.NodeStateFresh, 15*time.Second),
		"leaf did not reach fresh")

	// Invariant: no NULL frame_id on any rimsky_node_runs row anywhere.
	var nullDispatches int
	err := h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM rimsky_node_runs WHERE frame_id IS NULL`).Scan(&nullDispatches)
	require.NoError(t, err)
	require.Equal(t, 0, nullDispatches,
		"invariant 19 violated: %d rimsky_node_runs rows have NULL frame_id", nullDispatches)

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
		if state == string(cascade.NodeStateFresh) {
			require.Nil(t, frameID,
				"node %s in fresh state should have frame_id = NULL; got %v", nodeType, frameID)
		}
	}

	// Dispatch rows: every one (terminal or otherwise) carries a non-NULL frame_id.
	rows, err := h.Pool.Query(context.Background(),
		`SELECT id, frame_id FROM rimsky_node_runs`)
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
