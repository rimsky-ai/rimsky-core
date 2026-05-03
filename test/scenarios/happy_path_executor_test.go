// Scenario 1 — happy path: one executor-backed node runs to completion.
//
// Migrated to the stores-redesign template grammar (spec §11): nodes are
// built via scenario.MakeNode + the fluent option helpers. The redesign
// replaces the legacy "this resource has version N" assertion with
// "this node's rimsky_node_attributes.data contains field X"; this
// scenario keeps the original state-based assertion plus an attributes
// readback to demonstrate the new shape.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestHappyPathExecutor(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Stub returns an attributes_delta containing {"ok": true}; the
	// supervisor merges it into the node's resolved attributes.
	h.Stub.WhenType("worker").Complete(map[string]any{"ok": true}, true, "initial")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "happy-path", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-happy", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	// Verify a commit (or work_completed) event was appended.
	nid := n.ID
	evs, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
		persistence.ListPagination{Limit: 200}, nil)
	require.NoError(t, err)
	var sawCompleted bool
	for _, e := range evs.Events {
		if e.Kind == "work_completed" {
			sawCompleted = true
			break
		}
	}
	require.True(t, sawCompleted, "expected work_completed event")

	// Verify the executor's attributes_delta landed in
	// rimsky_node_attributes.data — the redesign's replacement for
	// "resource has version N" assertions.
	row, err := h.Persist.NodeAttributes().Get(h.Ctx, n.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, row, "expected node_attributes row to exist after commit")
	require.Equal(t, true, row.Data["ok"],
		"expected attributes.data.ok = true from executor's delta")
}
