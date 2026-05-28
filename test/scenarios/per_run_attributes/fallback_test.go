// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario test for the substitution fallback operator
// `{{<directive> | <literal>}}` under per-run attribute keying. When
// a directive misses (e.g. upstream hasn't run yet), the literal
// fires instead of failing template resolution.
//
// Per spec
// .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
// §"Fallback operator".
package per_run_attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestPerRunAttributes_FallbackOperator_LiteralFires verifies that a
// `{{params.absent | "default"}}` directive in an attribute schema
// resolves to the literal "default" when the directive misses, rather
// than failing template resolution.
func TestPerRunAttributes_FallbackOperator_LiteralFires(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-fallback", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"resolved": map[string]any{
							"type":   "string",
							"source": `{{params.absent | "fallback-fired"}}`,
						},
					},
					"required": []any{"resolved"},
				}),
			),
		},
	})
	// No params supplied — the directive misses, fallback fires.
	iid := h.CreateInstance(tid, "ck-fallback", map[string]any{})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)

	require.True(t, h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach fresh — fallback should resolve the missing directive")

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetMainRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, "fallback-fired", row.Data["resolved"],
		"fallback literal should resolve into attributes.data")
}
