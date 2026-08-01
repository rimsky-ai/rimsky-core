// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAttributeOverridesEndToEndDispatch(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attribute-overrides-e2e", Version: "1",
		Defaults: &node.TemplateDefaults{
			Attributes: &node.TemplateAttributeDefaults{
				ByExecutor: map[string]map[string]any{
					"stub": {
						"cli": map[string]any{
							"silence_timeout_ms": float64(60000),
							"trace_to":           "/template-default",
						},
					},
				},
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cli": map[string]any{
							"type": "object",
						},
						"ok": map[string]any{
							"type":     "boolean",
							"readOnly": true,
						},
					},
				}),
			),
		},
	})

	overrides := map[string]any{
		"by_executor": map[string]any{
			"stub": map[string]any{
				"cli": map[string]any{
					"trace_to":           "/by-executor",
					"synthetic_scenario": "exit-clean-no-callback",
				},
			},
		},
		"by_node": map[string]any{
			"worker": map[string]any{
				"cli": map[string]any{"trace_to": "/by-node"},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-ao-e2e", map[string]any{}, overrides)

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	got := waitForObservedAttrs(h, "worker")

	cli, ok := got["cli"].(map[string]any)
	require.True(t, ok, "attributes.cli missing or wrong shape: %#v", got)

	require.Equal(t, "/by-node", cli["trace_to"],
		"by_node should win the trace_to key (most specific layer)")
	require.Equal(t, "exit-clean-no-callback", cli["synthetic_scenario"],
		"by_executor should contribute synthetic_scenario")
	require.Equal(t, float64(60000), cli["silence_timeout_ms"],
		"template L1 default's silence_timeout_ms should be preserved")
}
