// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @decision: mode-default-most-recent
func TestMostRecentCoalescesCascades(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		Success(map[string]any{"counter": 1, "x": "r1"}, true, "a-r1").
		Then().Success(map[string]any{"counter": 2, "x": "r2"}, true, "a-r2").
		Then().Success(map[string]any{"counter": 3, "x": "r3"}, true, "a-r3").
		Then().Success(map[string]any{"counter": 4, "x": "r4"}, true, "a-r4").
		Then().Success(map[string]any{"counter": 5, "x": "r5"}, true, "a-r5")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "most-recent-coalesces-cascades", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "test/wake", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/counter/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"counter": map[string]any{"type": "integer", "readOnly": true},
						"x":       map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"counter", "x"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:        "b",
					Executor:    "stub",
					CascadeMode: string(cascade.CascadeModeMostRecent),
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/x/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_x": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.x}}",
						},
					},
					"required": []any{"snapshot_x"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-most-recent", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "most-recent-kick")

	bObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "b" {
				out = append(out, o)
			}
		}
		return out
	}

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if len(bObs()) >= 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	settleDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(settleDeadline) {
		require.LessOrEqual(t, len(bObs()), 1,
			"a duplicate b dispatch must not arrive during the post-coalesce settle window")
		time.Sleep(150 * time.Millisecond)
	}

	require.Equal(t, 1, len(bObs()),
		"under cascade_mode=most-recent, five cascade rounds from a (a1..a5 all emit "+
			"attribute/x/changed) must coalesce to a SINGLE dispatched b-run: at each "+
			"pending→stale transition, DeletePriorCascadeStales removes the earlier "+
			"stale-not-claimed cascade sibling. b must be invoked exactly once, seeing "+
			"a's LATEST value (r5).")

	require.Equal(t, "r5", bObs()[0].Attributes["snapshot_x"],
		"the single most-recent-coalesced b dispatch must see a's LATEST value")
}
