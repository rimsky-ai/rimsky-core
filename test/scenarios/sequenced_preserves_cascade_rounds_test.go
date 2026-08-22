// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
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
	releaseFirstB := make(chan struct{})
	h.Stub.WhenType("b").
		Success(map[string]any{}, true, "b-1").HoldUntil(releaseFirstB).
		Then().Success(map[string]any{}, true, "b-2").
		Then().Success(map[string]any{}, true, "b-3")

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
	bRunCount := func() int {
		var n int
		h.QuerySQL(`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1`,
			[]any{b.ID}, func(scan func(...any) error) error {
				return scan(&n)
			})
		return n
	}

	awaited.Until(t, "b's first dispatch to reach the executor and hold", func() bool {
		return h.Stub.Holding() >= 1
	})
	awaited.Until(t, "a's later rounds to queue two more b-runs behind the held dispatch", func() bool {
		return bRunCount() >= 3
	})
	close(releaseFirstB)

	awaited.Until(t, "all three sequenced b dispatches to reach the stub", func() bool { return len(bObs()) >= 3 })
	h.WaitForSchedulerQuiescence()
	require.Equal(t, 3, len(bObs()),
		"b must be invoked exactly three times — one per a self-cascade round in which "+
			"attribute/x/changed fired (a1: default→r1, a2: r1→r2, a3: r2→r3). Sequenced mode "+
			"preserves each cascade round as a distinct queued b-run; the fourth a-run "+
			"emits no attribute change so no b4 pending is created.")

	allB := bObs()
	expected := []string{"r1", "r2", "r3"}
	for i, obs := range allB {
		require.Equal(t, expected[i], obs.Attributes["snapshot_x"],
			"the executor sees a sequenced receiver's rounds in arrival order even when they "+
				"contest one dispatch: b's first dispatch held at the executor while a's later "+
				"rounds queued two more b-runs behind it, and each b-run resolves its bag from "+
				"the a-run that drove its round (b#1←a-r1, b#2←a-r2, b#3←a-r3). "+
				"got %v at dispatch #%d", obs.Attributes["snapshot_x"], i+1)
	}
}
