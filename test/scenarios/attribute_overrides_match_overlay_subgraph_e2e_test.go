// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAttributeOverridesMatchOverlaySubgraph_GraphMatcherRoutesByDispatchGraph(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cli": map[string]any{"type": "object"},
			"ok":  map[string]any{"type": "boolean", "readOnly": true},
		},
	})
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-graph-subgraph", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					{Type: "caller", Delegate: "worker"},
				},
			},
			{
				Name:  "worker",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-entry", Executor: "stub"},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{
							Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
				},
			},
		},
	})

	overrides := map[string]any{
		"by_match": []any{
			map[string]any{
				"matcher": map[string]any{"graph": "main"},
				"overlay": map[string]any{"cli": map[string]any{"where": "outer"}},
			},
			map[string]any{
				"matcher": map[string]any{"graph": "worker"},
				"overlay": map[string]any{"cli": map[string]any{"where": "inner"}},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-subgraph", map[string]any{}, overrides)

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing from instance")
	innerExitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, innerExitNode, "inner-exit node missing from instance")

	callerAttrs := waitForObservedAttrs(h, "caller")
	callerCLI, ok := callerAttrs["cli"].(map[string]any)
	require.True(t, ok, "caller attributes.cli missing or wrong shape: %#v", callerAttrs)
	require.Equal(t, "outer", callerCLI["where"],
		"caller lives in graph=main per concept:delegation; matcher graph=main MUST fire and NOT graph=worker")

	innerAttrs := waitForObservedAttrs(h, "inner-exit")
	innerCLI, ok := innerAttrs["cli"].(map[string]any)
	require.True(t, ok, "inner-exit attributes.cli missing or wrong shape: %#v", innerAttrs)
	require.Equal(t, "inner", innerCLI["where"],
		"inner-exit lives in graph=worker; matcher graph=worker MUST fire")

	require.Eventually(t, func() bool {
		c := attributeOverrideMatchCounts(t, h, iid, 2)
		return len(c) == 2 && c[0] == 1 && c[1] == 1
	}, 10*time.Second, 50*time.Millisecond,
		"AttributeOverridesMatchCounts mismatch (want [1, 1])")
}

func waitForObservedAttrs(h *scenario.Harness, nodeType string) map[string]any {
	for {
		for _, o := range h.Stub.Observed() {
			if o.NodeType == nodeType {
				return o.Attributes
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}
