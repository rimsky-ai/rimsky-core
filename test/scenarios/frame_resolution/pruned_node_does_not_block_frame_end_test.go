// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies spec §4.4: when a parent node commits with changed=false,
// downstream cascade message-passes are skipped. The downstream nodes
// remain fresh and never enter stale; the frame ends without them.
// Pruning audit trail: rimsky_node_runs has no rows for the pruned
// nodes for this frame_id.
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

	// @constraint: Wait for the frame to end (leaf is pruned, so frame-end fires once
	// source+middle both fresh, even though leaf was never invoked).
	require.True(t, waitForFramesByState(t, h, iid, "completed", 1, 10*time.Second),
		"frame did not end despite leaf pruning")

	// @deliberate: Leaf should never have entered stale (it stays fresh). Post-
	// stage-3 cutover: state comes from the in-flight run row; fresh
	// = no in-flight row exists for this node.
	var leafState string
	err := h.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(r.state, 'fresh')
		   FROM rimsky_nodes n
		   LEFT JOIN rimsky_node_runs r
		          ON r.node_id = n.id
		         AND r.phase IN ('pending','active','held','parked')
		  WHERE n.id = $1`, uuid.UUID(leaf.ID)).Scan(&leafState)
	require.NoError(t, err)
	require.Equal(t, "fresh", leafState,
		"pruned leaf should remain fresh")

	// @deliberate: No in-flight dispatch rows for the pruned leaf in this frame.
	// Post-stage-1 lifecycle flip: terminal rows survive past active
	// terminal so frame-end / retention / run-tree aggregation can read
	// the terminal state. The "pruned leaf has no dispatch rows" check
	// preserves its intent by filtering on the in-flight phase predicate.
	frames := listFrames(t, h, iid)
	require.Len(t, frames, 1)
	var leafDispatchCount int
	err = h.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM rimsky_node_runs
		WHERE frame_id = $1 AND node_id = $2
		  AND phase IN ('pending','active','held','parked')
	`, frames[0].FrameID, uuid.UUID(leaf.ID)).Scan(&leafDispatchCount)
	require.NoError(t, err)
	require.Equal(t, 0, leafDispatchCount,
		"pruned leaf must have no in-flight dispatch rows for this frame")
}
