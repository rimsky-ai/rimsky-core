// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attributes

import (
	"testing"
	"time"

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
						"maybe": map[string]any{"type": "string"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "lenient", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "upstream", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "upstream", Type: "attribute/maybe/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
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
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "upstream", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "upstream", Type: "attribute/maybe/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
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

	require.True(t, h.WaitForNodeState(upN.ID, cascade.NodeStateFresh, 20*time.Second),
		"upstream should settle fresh")
	require.True(t, h.WaitForNodeState(lenientN.ID, cascade.NodeStateFresh, 20*time.Second),
		"lenient node should reach terminal Complete (fresh), not fail with ErrMissingSource")

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

	require.True(t, h.WaitForNodeState(strictN.ID, cascade.NodeStateFailed, 20*time.Second),
		"strict node should fail the dispatch on the absent source (no `?` marker)")
	require.False(t, h.WaitForEventKind(strictN.ID, "terminal/success", 2*time.Second),
		"strict node must NOT reach a clean terminal Complete")
}
