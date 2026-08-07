// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: sequenced-preserves-cascade-rounds
func TestSequencedPreservesCascadeRounds(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		Success(map[string]any{"counter": 1, "x": "r1"}, true, "a-r1").
		Then().Success(map[string]any{"counter": 2, "x": "r2"}, true, "a-r2").
		Then().Success(map[string]any{"counter": 3, "x": "r3"}, true, "a-r3")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "sequenced-preserves-cascade-rounds", Version: "1",
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
					CascadeMode: spec.CascadeModeSequenced,
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
	iid := h.CreateInstance(tid, "ck-sequenced", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "sequenced-kick")

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
		if len(bObs()) >= 3 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.Equal(t, 3, len(bObs()),
		"b must be invoked exactly three times — one per a self-cascade round in which "+
			"attribute/x/changed fired (a1: default→r1, a2: r1→r2, a3: r2→r3). Sequenced mode "+
			"preserves each cascade round as a distinct queued b-run; the fourth a-run "+
			"emits no attribute change so no b4 pending is created.")

	allB := bObs()
	expected := []string{"r1", "r2", "r3"}
	for i, obs := range allB {
		require.Equal(t, expected[i], obs.Attributes["snapshot_x"],
			"sequenced mode preserves per-round bag content: each queued b-run resolves "+
				"its substituted bag from the specific a-run that drove its cascade round, "+
				"pinned via the wait-set sender_run_id at enqueue time (b#1←a-r1, b#2←a-r2, "+
				"b#3←a-r3), dispatched in sequence order. No two dispatches may see the same "+
				"bag. got %v at dispatch #%d", obs.Attributes["snapshot_x"], i+1)
	}
}
