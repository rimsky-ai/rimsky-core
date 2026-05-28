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

	// Each executor returns an attributes_delta carrying the field that
	// downstream nodes pull in via their `source: {{deps.<n>.<f>}}`
	// directives.
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
						// Post-spec-2026-05-19 Item 3: whole-directive
						// substitution lifts the JSON value at its native
						// type. The upstream a's `a` field is an integer,
						// so the receiver-side schema declares integer too.
						// The {{nodes.a.attribute.a}} ref auto-subscribes b
						// to a's `attribute` topic.
						"a": map[string]any{"type": "integer", "source": "{{nodes.a.attribute.a}}"},
						// Written by b's executor.
						"b": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// Post-spec-2026-05-19 Item 3: receiver schema
						// declares the upstream's native type (integer).
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

	// All three reach fresh on first run.
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 15*time.Second))

	// Verify the new data-flow path: b's attributes.data should contain
	// the `a` field (substituted from deps.a.a) and the `b` field
	// (written by b's executor delta).
	var bRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, b.ID, h.GetMainRunScopeID(iid), tx)
		bRow = r
		return err
	}))
	require.NotNil(t, bRow, "b should have a node_attributes row after fresh")
	require.Contains(t, bRow.Data, "a", "b.attributes.data should contain `a` from nodes.a.attribute.a")
	require.Contains(t, bRow.Data, "b", "b.attributes.data should contain `b` from executor delta")

	// Invalidate A; B and C should cascade-stale then rerun to fresh.
	resp, err := http.Post(h.ControlBase+"/nodes/"+a.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Expect B and C to eventually return to fresh (A runs again and cascades).
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 20*time.Second),
		"a did not re-reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 20*time.Second),
		"b did not re-reach fresh")
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 20*time.Second),
		"c did not re-reach fresh")
}
