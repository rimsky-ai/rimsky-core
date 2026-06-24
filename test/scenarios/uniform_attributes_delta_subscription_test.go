// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: uniform-attributes-delta-subscription
func TestUniformAttributesDeltaSubscription_Success(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("producer").
		Success(map[string]any{}, true, "").
		AttributesDelta(map[string]any{"trigger": "yes"})
	h.Stub.WhenType("handler").Success(map[string]any{}, true, "")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "uniform-delta-sub-success", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "producer", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "handler", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "producer",
					Type:                 "terminal/success",
					When:                 `payload.attributes_delta.trigger == "yes"`,
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-uniform-delta-sub-success", map[string]any{})
	p := h.FindNode(iid, "producer")
	hr := h.FindNode(iid, "handler")
	require.NotNil(t, p)
	require.NotNil(t, hr)

	require.True(t, h.WaitForNodeState(p.ID, cascade.NodeStateFresh, 15*time.Second),
		"producer did not reach fresh")
	require.True(t, h.WaitForNodeState(hr.ID, cascade.NodeStateFresh, 15*time.Second),
		"handler should fire on producer's terminal/success carrying payload.attributes_delta.trigger=yes")
	require.True(t, handlerDispatched(h, "handler"),
		"handler stub should have been dispatched at least once")
}

// @story: uniform-attributes-delta-subscription
func TestUniformAttributesDeltaSubscription_Error(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("producer").
		Error("foo", nil).
		AttributesDelta(map[string]any{"trigger": "yes"})
	h.Stub.WhenType("handler").Success(map[string]any{}, true, "")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "uniform-delta-sub-error", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "producer", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "handler", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "producer",
					Type:                 "terminal/error/*",
					When:                 `payload.attributes_delta.trigger == "yes"`,
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-uniform-delta-sub-error", map[string]any{})
	p := h.FindNode(iid, "producer")
	hr := h.FindNode(iid, "handler")
	require.NotNil(t, p)
	require.NotNil(t, hr)

	require.True(t, h.WaitForNodeState(p.ID, cascade.NodeStateFailed, 15*time.Second),
		"producer did not reach failed")
	require.True(t, h.WaitForNodeState(hr.ID, cascade.NodeStateFresh, 15*time.Second),
		"handler should fire on producer's terminal/error/* carrying payload.attributes_delta.trigger=yes")
	require.True(t, handlerDispatched(h, "handler"),
		"handler stub should have been dispatched at least once")
}

func handlerDispatched(h *scenario.Harness, nodeType string) bool {
	for _, obs := range h.Stub.Observed() {
		if obs.NodeType == nodeType {
			return true
		}
	}
	return false
}
