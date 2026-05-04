// Verifies spec §4.4: when a parent node commits with changed=false,
// downstream cascade message-passes are skipped. The downstream nodes
// remain fresh and never enter stale; the frame ends without them.
// Pruning audit trail: rimsky_dispatch has no rows for the pruned
// nodes for this frame_id.
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

func TestPrunedNodeDoesNotBlockFrameEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// source commits changed=true; middle commits changed=false → leaf is pruned.
	h.Stub.WhenType("source").Complete(map[string]any{}, true, "ok")
	h.Stub.WhenType("middle").Complete(map[string]any{}, false, "no-op")
	h.Stub.WhenType("leaf").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pruned-node", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "source", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "middle", Executor: "stub", Dependencies: []string{"source"}}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "leaf", Executor: "stub", Dependencies: []string{"middle"}}),
		},
	})
	iid := h.CreateInstance(tid, "ck-pruned", map[string]any{})
	source := h.FindNode(iid, "source")
	middle := h.FindNode(iid, "middle")
	leaf := h.FindNode(iid, "leaf")
	require.NotNil(t, source)
	require.NotNil(t, middle)
	require.NotNil(t, leaf)

	// Wait for source and middle to finish.
	require.True(t, h.WaitForNodeState(source.ID, shared.NodeStateFresh, 15*time.Second),
		"source did not finish")
	require.True(t, h.WaitForNodeState(middle.ID, shared.NodeStateFresh, 15*time.Second),
		"middle did not finish")

	// Wait for the frame to end (leaf is pruned, so frame-end fires once
	// source+middle both fresh, even though leaf was never invoked).
	require.True(t, waitForFramesByState(t, h, iid, "completed", 1, 10*time.Second),
		"frame did not end despite leaf pruning")

	// Leaf should never have entered stale (it stays fresh).
	var leafState string
	err := h.Pool.QueryRow(context.Background(),
		`SELECT state FROM rimsky_nodes WHERE id = $1`, uuid.UUID(leaf.ID)).Scan(&leafState)
	require.NoError(t, err)
	require.Equal(t, "fresh", leafState,
		"pruned leaf should remain fresh")

	// No dispatch rows for the pruned leaf in this frame.
	frames := listFrames(t, h, iid)
	require.Len(t, frames, 1)
	var leafDispatchCount int
	err = h.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM rimsky_dispatch
		WHERE frame_id = $1 AND node_id = $2
	`, frames[0].FrameID, uuid.UUID(leaf.ID)).Scan(&leafDispatchCount)
	require.NoError(t, err)
	require.Equal(t, 0, leafDispatchCount,
		"pruned leaf must have no dispatch rows for this frame")
}
