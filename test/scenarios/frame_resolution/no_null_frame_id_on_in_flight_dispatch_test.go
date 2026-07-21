// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_resolution

import (
	"context"
	"testing"

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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "worker", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)})),
			scenario.MakeNode(node.TemplateNodeDef{Type: "leaf", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "middle", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-no-null", map[string]any{})

	src := h.FindNode(iid, "worker")
	require.NotNil(t, src)
	leaf := h.FindNode(iid, "leaf")
	require.NotNil(t, leaf)

	h.WaitForNodeState(leaf.ID, cascade.NodeStateFresh)

	var nullDispatches int
	err := h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM rimsky_node_runs WHERE frame_id IS NULL`).Scan(&nullDispatches)
	require.NoError(t, err)
	require.Equal(t, 0, nullDispatches,
		"frame_id must not be NULL:%d rimsky_node_runs rows have NULL frame_id", nullDispatches)

	rows, err := h.Pool.Query(context.Background(),
		`SELECT id, frame_id FROM rimsky_node_runs`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var nodeRunID, frameID uuid.UUID
		require.NoError(t, rows.Scan(&nodeRunID, &frameID))
		require.NotEqual(t, uuid.Nil, frameID,
			"dispatch %s has zero frame_id", nodeRunID)
	}
	require.NoError(t, rows.Err())
}
