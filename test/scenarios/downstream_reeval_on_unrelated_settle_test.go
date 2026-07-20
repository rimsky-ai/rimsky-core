// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestDownstreamReevalOnUnrelatedSenderSettle(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	blockerHold := make(chan struct{})
	h.Stub.WhenType("trigger").Success(map[string]any{}, true, "trigger-ran").Delay(300 * time.Millisecond)
	h.Stub.WhenType("blocker").Success(map[string]any{}, true, "blocker-ran").HoldUntil(blockerHold)
	h.Stub.WhenType("receiver").Success(map[string]any{}, true, "receiver-ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "downstream-reeval-on-unrelated-settle", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "trigger", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "blocker", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "trigger", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "blocker", Type: "terminal/error/*",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-downstream-reeval", map[string]any{})
	trigger := h.FindNode(iid, "trigger")
	blocker := h.FindNode(iid, "blocker")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, trigger)
	require.NotNil(t, blocker)
	require.NotNil(t, receiver)

	h.WaitForNodeState(trigger.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(blocker.ID, cascade.NodeStateRunning)

	var receiverPending int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND state = 'pending'`,
		[]any{receiver.ID}, &receiverPending,
	)
	require.Equal(t, 1, receiverPending,
		"receiver's pending run must be created by trigger's terminal/success cascade edge, "+
			"then held pending by anySubscribedUpstreamInFlight because blocker is a declared "+
			"subscribed sender type for receiver (via the never-fired terminal/error/* edge) "+
			"and blocker is still in-flight; receiver must NOT have advanced past pending yet")

	close(blockerHold)
	h.WaitForNodeState(blocker.ID, cascade.NodeStateFresh)

	h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh)

	var receiverObserved int
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "receiver" {
			receiverObserved++
		}
	}
	require.Equal(t, 1, receiverObserved,
		"receiver must have been dispatched exactly once, driven by reevalDownstreamReceiverGates "+
			"re-checking receiver's still-pending gate when blocker settles — blocker's settle never "+
			"fires a terminal/error/* signal (it only ever succeeds), so no wait-set row links "+
			"blocker to receiver's run and the normal drain-by-sender path cannot find it; without "+
			"reevalDownstreamReceiverGates, receiver would stay pending forever")
}
