// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: cascade
// @concept: frame
func TestCascadeWalkCreatesNoFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		Success(map[string]any{"x": "r1"}, true, "a-r1").
		Then().Success(map[string]any{"x": "r2"}, true, "a-r2").
		Then().Success(map[string]any{"x": "r3"}, true, "a-r3")
	h.Stub.WhenType("b").Success(map[string]any{"y": "b"}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "c")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-walk-creates-no-frame", Version: "1",
		Messages: []spec.MessageSchema{{Type: "test/wake"}},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "test/wake", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/x/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"x"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/x/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_x": map[string]any{"type": "string", "source": "{{nodes.a.attribute.x}}"},
						"y":          map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"snapshot_x"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "b", Type: "attribute/y/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_y": map[string]any{"type": "string", "source": "{{nodes.b.attribute.y}}"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-walk-no-frame", map[string]any{})
	c := h.FindNode(iid, "c")
	require.NotNil(t, c)

	h.PostInstanceMessage(iid, "test/wake", nil, "walk-no-frame-kick")
	h.WaitForNodeState(c.ID, cascade.NodeStateFresh)
	h.WaitForSchedulerQuiescence()

	var frames, deliveredMessages, runs int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1`, []any{iid}, &frames)
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND frame_id IS NOT NULL`,
		[]any{iid}, &deliveredMessages)
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs r
	                 JOIN rimsky_nodes n ON n.id = r.node_id
	                WHERE n.instance_id = $1 AND r.creation_reason = 'cascade'`, []any{iid}, &runs)

	require.Positive(t, frames, "the instance must have run at least one frame")
	require.Equal(t, deliveredMessages, frames,
		"every frame traces to a message the frame engine picked up; the cascade walk creates none of its own")
	require.Greater(t, runs, frames,
		"the walk ran: it created more cascade-driven runs than there are frames, and still opened no frame")
}
