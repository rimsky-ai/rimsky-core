// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 5 — A → B → C all run to fresh; invalidating A cascades to
// B and C, both re-run.
//
// Migrated to the stores-redesign template grammar (spec §11): nodes are
// built via scenario.MakeNode + scenario.WithAttributes. Data flow between
// nodes uses the new attribute-source mechanism (spec §10): each
// downstream node's attribute schema declares fields with
// `source: "{{deps.<n>.<f>}}"`, which the supervisor substitutes at
// dispatch from the upstream node's rimsky_node_attributes.data row.
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

	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestCascadeInvalidate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Each executor returns an attributes_delta carrying the field that
	// downstream nodes pull in via their `source: {{deps.<n>.<f>}}`
	// directives.
	h.Stub.WhenType("a").Complete(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Complete(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Complete(map[string]any{"c": 1}, true, "c")

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
				node.TemplateNodeDef{Type: "b", Executor: "stub", Dependencies: []string{"a"}},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// Substitution always yields a stringified value
						// (spec §10), so source-driven fields are typed
						// as string regardless of the upstream's storage
						// type.
						"a": map[string]any{"type": "string", "source": "{{deps.a.a}}"},
						// Written by b's executor.
						"b": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub", Dependencies: []string{"b"}},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b": map[string]any{"type": "string", "source": "{{deps.b.b}}"},
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
	require.True(t, h.WaitForNodeState(a.ID, shared.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(b.ID, shared.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(c.ID, shared.NodeStateFresh, 15*time.Second))

	// Verify the new data-flow path: b's attributes.data should contain
	// the `a` field (substituted from deps.a.a) and the `b` field
	// (written by b's executor delta).
	bRow, err := h.Persist.NodeAttributes().Get(h.Ctx, b.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, bRow, "b should have a node_attributes row after fresh")
	require.Contains(t, bRow.Data, "a", "b.attributes.data should contain `a` from deps.a.a")
	require.Contains(t, bRow.Data, "b", "b.attributes.data should contain `b` from executor delta")

	// Invalidate A; B and C should cascade-stale then rerun to fresh.
	resp, err := http.Post(h.ControlBase+"/nodes/"+a.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Expect B and C to eventually return to fresh (A runs again and cascades).
	require.True(t, h.WaitForNodeState(a.ID, shared.NodeStateFresh, 20*time.Second),
		"a did not re-reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, shared.NodeStateFresh, 20*time.Second),
		"b did not re-reach fresh")
	require.True(t, h.WaitForNodeState(c.ID, shared.NodeStateFresh, 20*time.Second),
		"c did not re-reach fresh")
}
