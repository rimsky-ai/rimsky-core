// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attributes

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestLenientMarkerRecoveryE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("upstream").Success(map[string]any{"present": "yes"}, true, "ok")
	h.Stub.WhenType("lenient").Success(map[string]any{}, true, "ok")
	h.Stub.WhenType("strict").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "lenient-marker-recovery", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "upstream", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"present": map[string]any{"type": "string"},
						"maybe":   map[string]any{"type": "string"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "lenient", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "upstream", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "upstream", Type: "attribute/maybe/changed", ForceUpstreamRefresh: node.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"note": map[string]any{
							"type":   "string",
							"source": "{{nodes.upstream.attribute.maybe?}}",
						},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "strict",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"template_resolution_failed": {
							Action: "give_up",
						},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "upstream", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "upstream", Type: "attribute/maybe/changed", ForceUpstreamRefresh: node.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"note": map[string]any{
							"type":   "string",
							"source": "{{nodes.upstream.attribute.maybe}}",
						},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-lenient-recovery", map[string]any{})

	upN := h.FindNode(iid, "upstream")
	lenientN := h.FindNode(iid, "lenient")
	strictN := h.FindNode(iid, "strict")
	require.NotNil(t, upN)
	require.NotNil(t, lenientN)
	require.NotNil(t, strictN)

	h.WaitForNodeState(upN.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(lenientN.ID, cascade.NodeStateFresh)

	var lenientNote any
	var sawLenientDispatch bool
	for _, obs := range h.Stub.Observed() {
		if obs.NodeType == "lenient" {
			sawLenientDispatch = true
			lenientNote = obs.Attributes["note"]
		}
	}
	require.True(t, sawLenientDispatch, "lenient node should have dispatched to the stub")
	require.Equal(t, "", lenientNote,
		"lenient `?` directive over an absent source should resolve to empty string at dispatch")

	h.WaitForNodeState(strictN.ID, cascade.NodeStateFailed)
	require.False(t, h.HasEventKind(strictN.ID, "terminal/success"),
		"strict node must NOT reach a clean terminal Complete")
}
