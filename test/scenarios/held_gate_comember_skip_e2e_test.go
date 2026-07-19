// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHeldGateUpstreamComemberSkip(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	sideGate := make(chan struct{})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquirer-held")
	h.Stub.WhenType("side").Success(map[string]any{}, true, "side-done").HoldUntil(sideGate)
	h.Stub.WhenType("receiver").Success(map[string]any{}, true, "receiver-done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-gate-comember-skip", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "/region", "rw", "schema")),
			),
			scenario.MakeNode(node.TemplateNodeDef{Type: "side", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "receiver",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"schema": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", ForceUpstreamRefresh: spec.BoolPtr(false)},
					node.SubscriptionEntry{Node: "side", Type: "terminal/*", ForceUpstreamRefresh: spec.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"held_addr": map[string]any{"type": "string", "source": "{{claim.schema.address}}"},
					},
					"required": []any{"held_addr"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-gate-comember-skip", map[string]any{})
	acquirer := h.FindNode(iid, "acquirer")
	side := h.FindNode(iid, "side")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, acquirer)
	require.NotNil(t, side)
	require.NotNil(t, receiver)

	h.WaitForNodeState(acquirer.ID, cascade.NodeStateHeld)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND state NOT IN ('pending')`,
			[]any{receiver.ID}, &n)
		require.Zero(t, n,
			"receiver must not dispatch to completion while its non-subgraph upstream 'side' is still in flight, "+
				"even though its held co-member 'acquirer' has already settled held")
		time.Sleep(50 * time.Millisecond)
	}

	close(sideGate)

	h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh)

	var receiverRuns int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{receiver.ID}, &receiverRuns)
	require.Equal(t, 1, receiverRuns,
		"receiver must dispatch exactly once, unblocked purely by 'side' settling: the gate evaluator's "+
			"upstream-in-flight check must skip 'acquirer' (a held co-member of receiver's own holding "+
			"subgraph) rather than treating its Held state as a blocking in-flight upstream forever")

	var claimState string
	h.QueryRowSQL(`SELECT state FROM rimsky_claim_handles WHERE holder_node_id = $1`,
		[]any{acquirer.ID}, &claimState)
	require.Equal(t, string(spec.ClaimHandleStateCommitted), claimState,
		"acquirer's claim must commit once receiver (its only held co-member) completes")
}
