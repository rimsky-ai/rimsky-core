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

// @story: resume-preserves-snapshot
func TestResumePreservesSnapshot_DeadlineWakeReusesDispatchTimeBag(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").Success(map[string]any{"x": "initial"}, true, "a-first")
	resumeAt := time.Now().Add(10 * time.Second)
	h.Stub.WhenType("b").Park(genv1.ParkReason_PARK_REASON_SNOOZE, "deadline", resumeAt)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "resume-preserves-snapshot", Version: "1",
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

	iid := h.CreateInstance(tid, "ck-resume-preserves", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "test-wake-init")

	h.WaitForNodeState(b.ID, cascade.NodeStateParked)

	bInvocations := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, obs := range h.Stub.Observed() {
			if obs.NodeType == "b" {
				out = append(out, obs)
			}
		}
		return out
	}
	first := bInvocations()
	require.Equal(t, 1, len(first), "b should have been dispatched exactly once before park")
	require.Equal(t, "initial", first[0].Attributes["snapshot_x"],
		"b's first dispatch must see a's initial value for x")

	h.ExecSQL(
		`UPDATE rimsky_node_attributes SET data = jsonb_set(data, '{x}', '"updated"'::jsonb)
		 WHERE node_id = $1`,
		a.ID,
	)

	h.Stub.WhenType("b").Success(map[string]any{}, true, "b-resumed")

	h.WaitForEventKind(b.ID, "parked_resume_started")
	h.WaitForNodeState(b.ID, cascade.NodeStateFresh)

	allB := bInvocations()
	require.Equal(t, 2, len(allB),
		"b should have been dispatched exactly twice: once before park, once on deadline resume")
	require.Equal(t, "initial", allB[1].Attributes["snapshot_x"],
		"b's deadline-driven resume must re-invoke the executor with the dispatch-time substitution snapshot "+
			"(x=initial), NOT a freshly rebuilt context (x=updated, the value we wrote into a's "+
			"attribute bag while b was parked). If this fails, the resume path rebuilt substitution "+
			"from current upstream attribute values, violating `story:resume-preserves-snapshot`.")
}
