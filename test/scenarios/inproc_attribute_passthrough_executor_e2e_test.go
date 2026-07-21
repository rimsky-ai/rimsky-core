// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: inproc-utility-executor
package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestInprocAttributePassthroughExecutorE2E(t *testing.T) {
	t.Parallel()

	h := scenario.Start(t, scenario.HarnessOpts{})

	spec := node.TemplateSpec{
		Name:    "inproc-attribute-passthrough-demo",
		Version: "1.0.0",
		Nodes: []node.TemplateNodeDef{{
			Type: "passthrough",
			Kind: "attribute_passthrough",
			Attributes: &node.NodeAttributesDef{
				Schema: map[string]any{
					"type":                 "object",
					"additionalProperties": true,
					"properties": map[string]any{
						"flag":  map[string]any{"type": "boolean", "default": true},
						"name":  map[string]any{"type": "string", "default": "hello"},
						"count": map[string]any{"type": "integer", "default": 1},
					},
				},
			},
		}},
	}

	tid := h.DeployTemplate(spec)
	require.NotEmpty(t, tid, "template_id from DeployTemplate must be non-empty")

	iid := h.CreateInstance(tid, "ck-inproc-attribute-passthrough", map[string]any{})

	passthrough := h.FindNode(iid, "passthrough")
	require.NotNil(t, passthrough, "passthrough node missing from instance")

	h.WaitForNodeState(passthrough.ID, cascade.NodeStateFresh)

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, passthrough.ID, h.GetMainRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "attribute row must exist after terminal/success")
	require.Equal(t, true, row.Data["flag"], "schema default `flag` must pass through as an attribute")
	require.Equal(t, "hello", row.Data["name"], "schema default `name` must pass through as an attribute")
	require.EqualValues(t, 1, row.Data["count"], "schema default `count` must pass through as an attribute")
}
