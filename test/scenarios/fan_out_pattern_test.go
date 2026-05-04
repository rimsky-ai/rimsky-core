// Scenario 4 — root pure-cascade node fans out to N executor downstreams;
// when the root fires on a schedule, every downstream runs.
//
// Migrated to the stores-redesign template grammar (spec §11): nodes are
// constructed via scenario.MakeNode + the fluent helpers. The root is a
// pure-cascade scheduled node (no executor, no stores); each child is an
// executor-backed leaf with a per-node attributes schema documenting the
// shape its executor's attributes_delta is expected to take.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
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
			// Pure-cascade scheduled root; no executor / no stores.
			scenario.MakeNode(node.TemplateNodeDef{Type: "root", Schedule: "* * * * *"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_a", Executor: "stub", Dependencies: []string{"root"}},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_b", Executor: "stub", Dependencies: []string{"root"}},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"b": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_c", Executor: "stub", Dependencies: []string{"root"}},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"c": map[string]any{"type": "integer"}},
				}),
			),
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
