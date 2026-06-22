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

func TestPrunedNodeDoesNotBlockFrameEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("source").Success(map[string]any{}, true, "ok")
	h.Stub.WhenType("middle").Success(map[string]any{}, false, "no-op")
	h.Stub.WhenType("leaf").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pruned-node", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "source", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "middle", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "source", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)})),
			scenario.MakeNode(node.TemplateNodeDef{Type: "leaf", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "middle", Type: "terminal/success", When: "payload.changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-pruned", map[string]any{})
	source := h.FindNode(iid, "source")
	middle := h.FindNode(iid, "middle")
	leaf := h.FindNode(iid, "leaf")
	require.NotNil(t, source)
	require.NotNil(t, middle)
	require.NotNil(t, leaf)

	require.True(t, h.WaitForNodeState(source.ID, cascade.NodeStateFresh, 15*time.Second),
		"source did not finish")
	require.True(t, h.WaitForNodeState(middle.ID, cascade.NodeStateFresh, 15*time.Second),
		"middle did not finish")

	require.True(t, waitForFramesByState(t, h, iid, "completed", 1, 10*time.Second),
		"frame did not end despite leaf pruning")

	var leafState string
	err := h.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(r.state, 'fresh')
		   FROM rimsky_nodes n
		   LEFT JOIN rimsky_node_runs r
		          ON r.node_id = n.id
		         AND r.state IN ('pending','stale','running','held','parked')
		  WHERE n.id = $1`, uuid.UUID(leaf.ID)).Scan(&leafState)
	require.NoError(t, err)
	require.Equal(t, "fresh", leafState,
		"pruned leaf should remain fresh")

	frames := listFrames(t, h, iid)
	require.Len(t, frames, 1)
	var leafDispatchCount int
	err = h.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM rimsky_node_runs
		WHERE frame_id = $1 AND node_id = $2
		  AND state IN ('pending','stale','running','held','parked')
	`, frames[0].FrameID, uuid.UUID(leaf.ID)).Scan(&leafDispatchCount)
	require.NoError(t, err)
	require.Equal(t, 0, leafDispatchCount,
		"pruned leaf must have no in-flight dispatch rows for this frame")
}
