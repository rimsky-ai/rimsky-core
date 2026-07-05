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
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: cascade-defers-during-flight
func TestCascadeDefersDuringFlight_ParkedReceiverNotInterruptedByUpstreamRerun(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").Success(map[string]any{"x": "initial"}, true, "a-initial")
	resumeAt := time.Now().Add(8 * time.Second)
	h.Stub.WhenType("b").Park(genv1.ParkReason_PARK_REASON_SNOOZE, "deadline-wait", resumeAt)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-defers-during-flight", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"x": map[string]any{"type": "string"}},
					"required":   []any{"x"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
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
	iid := h.CreateInstance(tid, "ck-cascade-defers-during-flight", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "test-wake-1")

	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateParked, 30*time.Second),
		"b should park on its first dispatch")

	bObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "b" {
				out = append(out, o)
			}
		}
		return out
	}

	preCascade := bObs()
	require.Equal(t, 1, len(preCascade), "b should be invoked exactly once before the upstream re-run")
	require.Equal(t, "initial", preCascade[0].Attributes["snapshot_x"],
		"b's first dispatch must see a's initial value for x")

	aObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "a" {
				out = append(out, o)
			}
		}
		return out
	}
	h.Stub.WhenType("a").Success(map[string]any{"x": "updated"}, true, "a-updated")
	h.PostInstanceMessage(iid, "test/wake", nil, "test-wake-2")

	deadlineRerun := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadlineRerun) {
		if len(aObs()) >= 2 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.Equal(t, 2, len(aObs()), "a should be re-invoked once for the second message")

	require.Equal(t, 1, len(bObs()),
		"b's executor must not be re-invoked while b is parked — the cascade from a's re-run "+
			"must queue a NEW pending b-run, not mutate or re-dispatch the parked one")

	var rowCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND state IN ('pending','stale')`,
		[]any{b.ID}, &rowCount,
	)
	require.GreaterOrEqual(t, rowCount, 1,
		"there must be at least one queued cascade-driven b-run waiting for the parked predecessor to settle")

	h.Stub.WhenType("b").Success(map[string]any{}, true, "b-resumed")

	require.True(t, h.WaitForEventKind(b.ID, "parked_resume_started", 30*time.Second),
		"deadline sweep should wake the parked b-run")

	deadline := time.Now().Add(45 * time.Second)
	var observedAfter int
	for time.Now().Before(deadline) {
		observedAfter = len(bObs())
		if observedAfter >= 3 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.Equal(t, 3, observedAfter,
		"b should be invoked exactly three times in total: (1) the parked dispatch, (2) the "+
			"deadline-resume of the same run with the dispatch-time snapshot, (3) the queued "+
			"cascade-driven run dispatched after settle with a's updated value")

	allB := bObs()
	last := allB[len(allB)-1]
	require.Equal(t, "updated", last.Attributes["snapshot_x"],
		"the cascade-driven post-settle dispatch must see a's POST-rerun value (updated), "+
			"proving the cascade was incorporated into the new bag")
}
