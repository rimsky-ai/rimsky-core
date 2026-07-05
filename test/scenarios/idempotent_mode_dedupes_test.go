// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
func TestIdempotentModeDedupes_QueueComparison(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		Success(map[string]any{"counter": 1, "stable": "same"}, true, "a-r1").
		Then().Success(map[string]any{"counter": 2, "stable": "same"}, true, "a-r2").
		Then().Success(map[string]any{"counter": 3, "stable": "same"}, true, "a-r3").
		Then().Success(map[string]any{"counter": 4, "stable": "same"}, true, "a-r4")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "idempotent-queue-dedupes", Version: "1",
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
						"counter": map[string]any{"type": "integer"},
						"stable":  map[string]any{"type": "string"},
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
	iid := h.CreateInstance(tid, "ck-idempotent-queue", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "idempotent-kick")

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
	time.Sleep(3 * time.Second)

	require.Equal(t, 1, len(bObs()),
		"under cascade_mode=idempotent-queue, a's four cascade rounds all produce a b input "+
			"bag of {snapshot_stable: \"same\"} (byte-identical). The gate evaluator's "+
			"modeDropIfPriorEqual JCS-compares each new pending's resolved bag against the "+
			"prior cascade stale's bag; when equal, the new pending is DROPPED. Only the "+
			"first cascade survives to a dispatch; the rest dedup at pending→stale. b must "+
			"be invoked exactly once.")

	require.Equal(t, "same", bObs()[0].Attributes["snapshot_stable"],
		"the single b dispatch must see a's stable value")
}
