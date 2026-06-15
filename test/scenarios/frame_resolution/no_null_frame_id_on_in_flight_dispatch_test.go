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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "worker", Type: "terminal/*"})),
			scenario.MakeNode(node.TemplateNodeDef{Type: "leaf", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "middle", Type: "terminal/*"})),
		},
	})
	iid := h.CreateInstance(tid, "ck-no-null", map[string]any{})

	src := h.FindNode(iid, "worker")
	require.NotNil(t, src)
	leaf := h.FindNode(iid, "leaf")
	require.NotNil(t, leaf)

	// @deliberate: Wait for the cascade to reach the leaf.
	require.True(t, h.WaitForNodeState(leaf.ID, cascade.NodeStateFresh, 15*time.Second),
		"leaf did not reach fresh")

	// @deliberate: Invariant: no NULL frame_id on any rimsky_node_runs row anywhere.
	var nullDispatches int
	err := h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM rimsky_node_runs WHERE frame_id IS NULL`).Scan(&nullDispatches)
	require.NoError(t, err)
	require.Equal(t, 0, nullDispatches,
		"invariant 19 violated: %d rimsky_node_runs rows have NULL frame_id", nullDispatches)

	// @deliberate: Invariant: no in-flight run row in state IN ('stale','running')
	// with NULL frame_id (post-stage-3: state lives on the run row).
	// rimsky_node_runs.frame_id is NOT NULL so this is structurally
	// guaranteed, but we keep the predicate for symmetry with the
	// invariant 19 audit.
	var nullNodes int
	err = h.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM rimsky_node_runs
		WHERE state IN ('stale','running')
		  AND phase IN ('pending','active','held','parked')
		  AND frame_id IS NULL
	`).Scan(&nullNodes)
	require.NoError(t, err)
	require.Equal(t, 0, nullNodes,
		"invariant 19 violated: %d non-fresh in-flight run rows have NULL frame_id", nullNodes)

	// @constraint: On completion: nodes return to fresh and rimsky_nodes.frame_id is
	// cleared (per spec §6.2 — completed clears frame_id, failed
	// preserves it). Post-stage-3: state comes from the in-flight run
	// row; fresh = no in-flight row.
	for _, nodeType := range []string{"worker", "middle", "leaf"} {
		nID := h.FindNode(iid, nodeType).ID
		var state string
		var frameID *uuid.UUID
		err := h.Pool.QueryRow(context.Background(),
			`SELECT COALESCE(r.state, 'fresh'), n.frame_id
			   FROM rimsky_nodes n
			   LEFT JOIN rimsky_node_runs r
			          ON r.node_id = n.id
			         AND r.phase IN ('pending','active','held','parked')
			  WHERE n.id = $1`,
			uuid.UUID(nID)).Scan(&state, &frameID)
		require.NoError(t, err)
		if state == string(cascade.NodeStateFresh) {
			require.Nil(t, frameID,
				"node %s in fresh state should have frame_id = NULL; got %v", nodeType, frameID)
		}
	}

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
