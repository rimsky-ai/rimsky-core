// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: sub-graph
// @concept: node
// @decision: kind-sugar-resolver
func TestTemplateSubGraphBuiltinKindNodeDispatches(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-entry").Success(map[string]any{"ok": true}, true, "entry")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-builtin-kind-dispatch", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
					),
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
						node.TemplateNodeDef{Type: "inner-builtin", Kind: "attribute_passthrough",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-builtin", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-subgraph-builtin-kind", map[string]any{})

	builtinNode := h.FindNode(iid, "inner-builtin")
	require.NotNil(t, builtinNode, "inner-builtin node missing")
	exitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, exitNode, "inner-exit node missing")
	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")

	h.WaitForNodeState(builtinNode.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(callerNode.ID, cascade.NodeStateFresh)

	var executorName string
	h.QueryRowSQL(`SELECT COALESCE(executor_name, '') FROM rimsky_node_runs WHERE node_id = $1
		 ORDER BY enqueued_at DESC LIMIT 1`,
		[]any{builtinNode.ID}, &executorName)
	require.NotEmpty(t, executorName,
		"the sub-graph node declared by builtin kind must dispatch against a resolved executor: "+
			"kind sugar resolves over every declared node, not only the flattened main graph")

	h.WaitForSettledFrameCount(iid, 1)
}
