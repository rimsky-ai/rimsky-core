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
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: cascade-defers-during-flight
func TestCascadeDefersDuringFlight_WalkerQueuesNewPendingWithoutMutatingInFlight(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		Success(map[string]any{"counter": 1, "x": "r1"}, true, "a-r1").
		Then().Success(map[string]any{"counter": 2, "x": "r2"}, true, "a-r2").
		Then().Success(map[string]any{"counter": 3, "x": "r3"}, true, "a-r3")

	resumeAt := time.Now().Add(6 * time.Second)
	h.Stub.WhenType("b").
		Park(resumeAt).
		Then().Success(map[string]any{}, true, "b-r1-resumed").
		Then().Success(map[string]any{}, true, "b-r2").
		Then().Success(map[string]any{}, true, "b-r3").
		Then().Success(map[string]any{}, true, "b-r4")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-defers-during-flight", Version: "1",
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
						Node: "a", Type: "terminal/success",
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
	iid := h.CreateInstance(tid, "ck-defers-during-flight", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "defers-during-flight-kick")

	h.WaitForNodeState(b.ID, cascade.NodeStateParked)

	bObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "b" {
				out = append(out, o)
			}
		}
		return out
	}

	require.Equal(t, 1, len(bObs()),
		"b's parked run must be the ONLY b invocation while parked — the walker created "+
			"b2, b3 as NEW pending runs for a2/a3 cascade rounds, not mutations of b1")

	var pendingBRuns int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_node_runs
		  WHERE node_id = $1 AND state IN ('pending','stale') AND creation_reason = 'cascade'`,
		[]any{b.ID}, &pendingBRuns,
	)
	require.GreaterOrEqual(t, pendingBRuns, 2,
		"b2, b3 must be queued (pending or stale) behind the parked b1 — proving the walker "+
			"created NEW cascade-driven runs for a's later self-cascade rounds rather than "+
			"mutating b1's in-flight state. got %d queued", pendingBRuns)

	h.WaitForEventCount(b.ID, "parked_resume_started", 1)

	awaited.Until(t, "all five b invocations to reach the stub", func() bool { return len(bObs()) >= 5 })
	h.WaitForSchedulerQuiescence()
	observedAfter := len(bObs())
	require.Equal(t, 5, observedAfter,
		"b must be invoked exactly five times in total: (1) b1 park, (2) b1 deadline-resume, "+
			"(3) b2, (4) b3, (5) b4 — one queued cascade-driven b-run per a self-cascade "+
			"round (a settled four times: a1 with r1 diff, a2 with r2 diff, a3 with r3 diff, "+
			"a4 with same r3 emitting terminal/success only; each terminal/success creates a "+
			"new cascade-driven b-run under sequenced mode). Fewer means the walker mutated "+
			"an in-flight run; more means it created a duplicate.")
}
