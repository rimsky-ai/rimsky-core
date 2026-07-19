// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHeldClaimMixedUpstream(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
			},
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
	h.Stub.WhenType("a").Success(map[string]any{}, true, "a-should-not-run")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c-ran")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b-should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-mixed-upstream", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "a",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Action: "pass",
						},
					},
				},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "@queue", "rw", "held")),
			),
			scenario.MakeNode(node.TemplateNodeDef{Type: "c", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "b",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"template_resolution_failed": {
							Action: "give_up",
						},
					},
					Holds: map[string]node.HoldsBinding{
						"held": {From: "a"},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"held_addr": map[string]any{
							"type":   "string",
							"source": "{{claim.held.address}}",
						},
					},
					"required": []any{"held_addr"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-mixed", map[string]any{})

	a := h.FindNode(iid, "a")
	bNode := h.FindNode(iid, "b")
	c := h.FindNode(iid, "c")
	require.NotNil(t, a)
	require.NotNil(t, bNode)
	require.NotNil(t, c)

	waitForSettlingSignalTypePrefix(t, h, a.ID, "terminal/error/")
	waitForSettlingSignalType(t, h, c.ID, "terminal/success")

	h.WaitForNodeState(bNode.ID, cascade.NodeStateFailed)

	var bLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, bNode.ID)
		bLatest = r
		return err
	}))
	require.NotNil(t, bLatest)
	require.NotNil(t, bLatest.SettlingSignalType,
		"b should have a settling_signal_type after give_up")
	require.Contains(t, *bLatest.SettlingSignalType, "terminal/error/",
		"b's give_up should record a terminal/error/<class> settling_signal_type")
}
