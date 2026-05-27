// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — per-instance attribute_overrides drives end-to-end:
//   - POST /instances accepts attribute_overrides
//   - persistence round-trips it on rimsky_instances.attribute_overrides
//   - acquisition reads it onto acquisition.InstanceAttributeOverrides
//   - applyAttributeOverrides deep-merges L1 template defaults (folded
//     into the effective schema's `default:` values at registration) +
//     L3 by_executor + L4 by_node fragments on top of resolved sources
//   - the merged map reaches the executor on ExecuteRequest.attributes
//
// This test guards against regressions of the "load-bearing seam" between
// the persisted column and the dispatch path. Before the 2026-05-21
// userdata-collapse rewrite, this scenario asserted on the userdata
// field; post-collapse it asserts on attributes.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
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
						// `cli` carries no L2 declaration so L1's
						// template-default ({silence_timeout_ms, trace_to})
						// folds into the effective schema as the
						// `default:` for the property at registration.
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
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	// Find the stub's record of the worker dispatch and assert the
	// attribute bag reaching the executor was the merged map: by_node
	// wins the trace_to key (most specific), by_executor contributes
	// synthetic_scenario, the template L1 default's silence_timeout_ms
	// is preserved.
	var got map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "worker" {
				got = o.Attributes
				break
			}
		}
		if got != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotNil(t, got, "stub did not record any worker dispatch")

	cli, ok := got["cli"].(map[string]any)
	require.True(t, ok, "attributes.cli missing or wrong shape: %#v", got)

	// by_node wins for keys present in both by_executor and by_node.
	require.Equal(t, "/by-node", cli["trace_to"],
		"by_node should win the trace_to key (most specific layer)")
	// by_executor contributes a key absent from base + by_node.
	require.Equal(t, "exit-clean-no-callback", cli["synthetic_scenario"],
		"by_executor should contribute synthetic_scenario")
	// L1 template default's silence_timeout_ms key not touched by either override.
	require.Equal(t, float64(60000), cli["silence_timeout_ms"],
		"template L1 default's silence_timeout_ms should be preserved")
}
