// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario test for the embedded-source grammar relaxed by the
// 2026-05-21 userdata-collapse spec: a single `source:` string may
// contain literal text alongside one or more `{{...}}` directives, and
// each directive admits the `| <literal>` fallback operator
// independently.
//
// Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Substitution grammar".
package per_run_attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

// TestPerRunAttributes_EmbeddedSource_LiteralAndDirectives verifies
// that an embedded `source:` string mixing literal text with a
// `params.*` directive plus a `| <literal>` fallback on a missing
// param resolves to the fully-composed string the executor receives at
// dispatch.
func TestPerRunAttributes_EmbeddedSource_LiteralAndDirectives(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-embedded-source", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"domain": map[string]any{"type": "string"},
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// Embedded source: literal head + params directive +
						// literal middle + missing-with-fallback directive +
						// literal tail.
						"prompt": map[string]any{
							"type":   "string",
							"source": `Generate config for {{params.domain}}. Notes: {{params.notes | "none"}}. Done.`,
						},
					},
					"required": []any{"prompt"},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-embedded", map[string]any{
		"domain": "alpha",
		// `notes` deliberately omitted; the directive's fallback should fire.
	})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)

	require.True(t, h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach fresh — embedded source + fallback should compose cleanly")

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, "Generate config for alpha. Notes: none. Done.", row.Data["prompt"],
		"embedded source should resolve to the composed string with the fallback firing for the missing param")
}
