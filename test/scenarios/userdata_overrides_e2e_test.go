// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — per-instance userdata_overrides drives end-to-end:
//   - POST /instances accepts userdata_overrides
//   - persistence round-trips it on rimsky_instances.userdata_overrides
//   - acquisition reads it onto acquisition.InstanceUserdataOverrides
//   - applyUserdataOverrides deep-merges template userdata + by_executor + by_node fragments
//   - the merged map reaches the executor on ExecuteRequest.userdata
//
// This test guards against regressions of the "load-bearing seam" between
// the persisted column and the dispatch path. Before the fix, the
// success-path acquisition struct omitted InstanceUserdataOverrides and
// the entire feature was a silent no-op on dispatch.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestUserdataOverridesEndToEndDispatch(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Complete(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "userdata-overrides-e2e", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					Userdata: map[string]any{
						"cli": map[string]any{
							"silence_timeout_ms": float64(60000),
							"trace_to":           "/template-default",
						},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean"},
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
	iid := h.CreateInstanceWithOverrides(tid, "ck-uo-e2e", map[string]any{}, overrides)

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	// Find the stub's record of the worker dispatch and assert the
	// userdata reaching the executor was the merged map: by_node wins
	// the trace_to key (most specific), by_executor contributes
	// synthetic_scenario, the template's silence_timeout_ms is
	// preserved.
	var got map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "worker" {
				got = o.Userdata
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
	require.True(t, ok, "userdata.cli missing or wrong shape: %#v", got)

	// by_node wins for keys present in both by_executor and by_node.
	require.Equal(t, "/by-node", cli["trace_to"],
		"by_node should win the trace_to key (most specific layer)")
	// by_executor contributes a key absent from base + by_node.
	require.Equal(t, "exit-clean-no-callback", cli["synthetic_scenario"],
		"by_executor should contribute synthetic_scenario")
	// template's per-node userdata key not touched by either override.
	require.Equal(t, float64(60000), cli["silence_timeout_ms"],
		"template's silence_timeout_ms should be preserved")
}
