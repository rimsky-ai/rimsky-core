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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "source", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)})),
			scenario.MakeNode(node.TemplateNodeDef{Type: "leaf", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "middle", Type: "terminal/success", When: "payload.changed", ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-pruned", map[string]any{})
	source := h.FindNode(iid, "source")
	middle := h.FindNode(iid, "middle")
	leaf := h.FindNode(iid, "leaf")
	require.NotNil(t, source)
	require.NotNil(t, middle)
	require.NotNil(t, leaf)

	h.WaitForNodeState(source.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(middle.ID, cascade.NodeStateFresh)

	waitForFramesByState(t, h, iid, "completed", 1)

	frames := listFrames(t, h, iid)
	require.Len(t, frames, 1)
	var leafDispatchCount int
	err := h.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM rimsky_node_runs
		WHERE frame_id = $1 AND node_id = $2
		  AND state IN ('pending','stale','running','held','parked')
	`, frames[0].FrameID, uuid.UUID(leaf.ID)).Scan(&leafDispatchCount)
	require.NoError(t, err)
	require.Equal(t, 0, leafDispatchCount,
		"pruned leaf must have no in-flight dispatch rows for this frame")
}
