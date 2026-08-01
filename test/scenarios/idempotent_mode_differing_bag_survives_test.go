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

// @story: idempotent-mode-dedupes
func TestIdempotentModeQueueComparison_DifferingBagSurvives(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		Success(map[string]any{"counter": 1, "stable": "v1"}, true, "a-r1").
		Then().Success(map[string]any{"counter": 2, "stable": "v2"}, true, "a-r2").
		Then().Success(map[string]any{"counter": 3, "stable": "v3"}, true, "a-r3")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "idempotent-queue-differing-bag-survives", Version: "1",
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
						"stable":  map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"counter", "stable"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:        "b",
					Executor:    "stub",
					CascadeMode: string(cascade.CascadeModeIdempotentQueue),
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "a", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/stable/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_stable": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.stable}}",
						},
					},
					"required": []any{"snapshot_stable"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-idempotent-queue-differs", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "idempotent-differs-kick")

	bObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "b" {
				out = append(out, o)
			}
		}
		return out
	}

	for len(bObs()) < 3 {
		time.Sleep(50 * time.Millisecond)
	}
	waitForRunCountSettled(h, a.ID, 4)
	h.WaitForAllRunsTerminal(b.ID)

	require.Equal(t, 3, len(bObs()),
		"under cascade_mode=idempotent-queue, a's three cascade rounds each produce a "+
			"DIFFERENT resolved bag for b ({snapshot_stable: v1}, then v2, then v3). "+
			"modeDropIfPriorEqual's JCS comparison against the prior cascade stale's bag "+
			"must report unequal every time, so none of the three pendings are dropped: "+
			"b must be invoked exactly three times, once per distinct bag")

	seen := map[string]bool{}
	for _, o := range bObs() {
		seen[o.Attributes["snapshot_stable"].(string)] = true
	}
	require.True(t, seen["v1"] && seen["v2"] && seen["v3"],
		"each of the three b dispatches must carry a's distinct per-round snapshot_stable value; got %v", bObs())
}
