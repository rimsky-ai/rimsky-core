// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "middle", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "worker", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)})),
			scenario.MakeNode(node.TemplateNodeDef{Type: "leaf", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "middle", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-no-null", map[string]any{})

	src := h.FindNode(iid, "worker")
	require.NotNil(t, src)
	leaf := h.FindNode(iid, "leaf")
	require.NotNil(t, leaf)

	require.True(t, h.WaitForNodeState(leaf.ID, cascade.NodeStateFresh, 15*time.Second),
		"leaf did not reach fresh")

	var nullDispatches int
	err := h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM rimsky_node_runs WHERE frame_id IS NULL`).Scan(&nullDispatches)
	require.NoError(t, err)
	require.Equal(t, 0, nullDispatches,
		"frame_id must not be NULL:%d rimsky_node_runs rows have NULL frame_id", nullDispatches)

	var nullNodes int
	err = h.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM rimsky_node_runs
		WHERE state IN ('stale','running')
		  AND state IN ('pending','stale','running','held','parked')
		  AND frame_id IS NULL
	`).Scan(&nullNodes)
	require.NoError(t, err)
	require.Equal(t, 0, nullNodes,
		"frame_id must not be NULL:%d non-fresh in-flight run rows have NULL frame_id", nullNodes)

	for _, nodeType := range []string{"worker", "middle", "leaf"} {
		nID := h.FindNode(iid, nodeType).ID
		var state string
		var frameID *uuid.UUID
		err := h.Pool.QueryRow(context.Background(),
			`SELECT COALESCE(r.state, 'fresh'), n.frame_id
			   FROM rimsky_nodes n
			   LEFT JOIN rimsky_node_runs r
			          ON r.node_id = n.id
			         AND r.state IN ('pending','stale','running','held','parked')
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
