// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Substantive scenario coverage for attributes substitution at
// dispatch under the stores redesign: a `{{params.key}}` directive in
// the attributes schema's `source:` is resolved at dispatch and lands
// in the attributes payload that the supervisor persists alongside
// any executor-supplied delta.
//
// Targets blessed invariants 11 (userdata opaque — never substituted)
// and 12 (attributes validate twice).
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

// TestParamsSubstitutionAtDispatch verifies the supervisor substitutes
// {{params.foo}} into the attributes object before invoking the
// executor. Post-terminal, rimsky_node_attributes.data must carry the
// resolved value alongside any executor-supplied fields.
func TestParamsSubstitutionAtDispatch(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("greeter").Success(map[string]any{"executor_field": "from-executor"}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "substitution-dispatch", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "greeter", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"greeting":       map[string]any{"type": "string", "source": "{{params.greeting}}"},
						"executor_field": map[string]any{"type": "string"},
					},
					"required": []any{"greeting"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-subst", map[string]any{"greeting": "hello-world"})

	g := h.FindNode(iid, "greeter")
	require.NotNil(t, g)
	require.True(t, h.WaitForNodeState(g.ID, cascade.NodeStateFresh, 15*time.Second))

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().Get(h.Ctx, g.ID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, "hello-world", row.Data["greeting"], "params substitution should resolve at dispatch")
	require.Equal(t, "from-executor", row.Data["executor_field"], "executor delta should merge into final attributes")
}

// TestRequiredFieldMissingParamFailsTemplateResolution verifies that a
// required source-driven attribute whose param is absent fires the
// template_resolution_failed policy chain — exercises the
// attributes.PhaseDispatch validation gate at the supervisor.
func TestRequiredFieldMissingParamFailsTemplateResolution(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "missing-param", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "needs-param",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"template_resolution_failed": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"req": map[string]any{"type": "string", "source": "{{params.absent}}"},
					},
					"required": []any{"req"},
				}),
			),
		},
	})
	// No params supplied — the required source-driven directive will
	// raise ErrMissingSource at the dispatch substitution pass.
	iid := h.CreateInstance(tid, "ck-missing-param", map[string]any{})

	n := h.FindNode(iid, "needs-param")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFailed, 15*time.Second),
		"missing required substitution should drive node to failed via give_up")
}
