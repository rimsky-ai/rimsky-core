// Scenario 4 — root pure-cascade node fans out to N executor downstreams;
// when the root fires on a schedule, every downstream runs.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestFanOutPattern(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("child_a").Complete(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("child_b").Complete(map[string]any{"b": 2}, true, "b")
	h.Stub.WhenType("child_c").Complete(map[string]any{"c": 3}, true, "c")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fan-out", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "root", Schedule: "* * * * *"}, // pure-cascade root
			{Type: "child_a", Executor: "stub", Dependencies: []string{"root"}},
			{Type: "child_b", Executor: "stub", Dependencies: []string{"root"}},
			{Type: "child_c", Executor: "stub", Dependencies: []string{"root"}},
		},
	})
	iid := h.CreateInstance(tid, "ck-fanout", map[string]any{})

	root := h.FindNode(iid, "root")
	require.NotNil(t, root)

	// Force schedule fire.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_schedules SET next_fire_at = NOW() - INTERVAL '1 second' WHERE node_id = $1`,
		root.ID,
	)
	require.NoError(t, err)

	// All three children should reach fresh.
	for _, typ := range []string{"child_a", "child_b", "child_c"} {
		c := h.FindNode(iid, typ)
		require.NotNil(t, c, "missing %s", typ)
		require.True(t, h.WaitForNodeState(c.ID, shared.NodeStateFresh, 30*time.Second),
			"%s did not reach fresh", typ)
	}
}
