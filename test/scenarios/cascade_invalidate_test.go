// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 5 — A → B → C all run to fresh; invalidating A cascades to
// B and C, both re-run.
//
// Migrated to the post-2026-05-14 subscription-cascade template grammar:
// nodes are built via scenario.MakeNode + scenario.WithAttributes. Data
// flow between nodes uses the substitution grammar
// `source: "{{nodes.<X>.attribute.<Y>}}"`, which auto-subscribes the
// receiver to the sender's `attribute` topic at template-registration
// time (see graph/node/subscription_edges.go).
//
// The behavioural intent (chain reaches fresh; invalidate-A cascades to
// B and C) is preserved; the redesign-shaped assertion ("this node's
// attributes.data contains field X") replaces the legacy
// "this resource has version N" pattern.
package scenarios

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestCascadeInvalidate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "chain", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a": map[string]any{"type": "integer", "source": "{{nodes.a.attribute.a}}"},
						"b": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b": map[string]any{"type": "integer", "source": "{{nodes.b.attribute.b}}"},
						"c": map[string]any{"type": "integer"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-chain", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	c := h.FindNode(iid, "c")
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.NotNil(t, c)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 15*time.Second))

	var bRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, b.ID, h.GetMainRunScopeID(iid), tx)
		bRow = r
		return err
	}))
	require.NotNil(t, bRow, "b should have a node_attributes row after fresh")
	require.Contains(t, bRow.Data, "a", "b.attributes.data should contain `a` from nodes.a.attribute.a")
	require.Contains(t, bRow.Data, "b", "b.attributes.data should contain `b` from executor delta")

	resp, err := http.Post(h.ControlBase+"/v1/nodes/"+a.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 20*time.Second),
		"a did not re-reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 20*time.Second),
		"b did not re-reach fresh")
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 20*time.Second),
		"c did not re-reach fresh")
}
