// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attributes

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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
						"count":          map[string]any{"type": "integer", "source": "{{params.count}}"},
						"tagged_id":      map[string]any{"type": "string", "source": "id-{{params.big_id}}-tag"},
					},
					"required": []any{"greeting"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-subst", map[string]any{
		"greeting": "hello-world",
		"count":    1700000000,
		"big_id":   1700000000,
	})

	g := h.FindNode(iid, "greeter")
	require.NotNil(t, g)
	h.WaitForNodeState(g.ID, cascade.NodeStateFresh)

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, g.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, "hello-world", row.Data["greeting"], "params substitution should resolve at dispatch")
	require.Equal(t, "from-executor", row.Data["executor_field"], "executor delta should merge into final attributes")
	require.EqualValues(t, 1700000000, row.Data["count"],
		"a bare numeric directive must substitute the exact numeric value, not a stringified/rounded form")
	require.Equal(t, "id-1700000000-tag", row.Data["tagged_id"],
		"a numeric param embedded in a larger string source must render as plain decimal digits, "+
			"not Go's default scientific-notation float formatting (e.g. 1.7e+09)")
}

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
							Action: "give_up",
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
	iid := h.CreateInstance(tid, "ck-missing-param", map[string]any{})

	n := h.FindNode(iid, "needs-param")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFailed)
}
