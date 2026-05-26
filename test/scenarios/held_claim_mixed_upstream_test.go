// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 30 — held_claim_mixed_upstream.
//
// A three-node template: A acquires a held claim from a stub queue
// (passes via error_types: { "acquire/unavailable": [pass] }); C is
// an independent upstream of B that commits Changed=true; B inherits
// the held claim from A AND depends on C.
//
// Expected behavior:
//   - C cascades to B; B dispatches.
//   - B's substitution into {{claim.held.address}} fails because A
//     never acquired the claim.
//   - template_resolution_failed routes through B's error_types policy
//     (give_up); B lands in failed.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/control/config"
	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
	"github.com/fallguyconsulting/rimsky/sdk/go/stores/action"
	stubstore "github.com/fallguyconsulting/rimsky/stores/stub/store"
	stubfixture "github.com/fallguyconsulting/rimsky/stores/stub/testfixture"
)

func TestHeldClaimMixedUpstream(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// Empty queue — A's Open returns Unavailable.
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("a").Success(map[string]any{}, true, "a-should-not-run")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c-ran")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b-should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-mixed-upstream", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "a",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{{Action: "pass"}},
						},
					},
				},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "@queue", "rw", "held")),
			),
			scenario.MakeNode(node.TemplateNodeDef{Type: "c", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "b",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"template_resolution_failed": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithInherits(scenario.Inherit("held")),
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "a", Type: "terminal/*"},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*"},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// Source-driven attribute that requires A to have
						// acquired the held claim. When A passes, this
						// substitution fails → template_resolution_failed.
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

	// A passes (settling_signal_type=terminal/error/<class>); C commits
	// (settling_signal_type=terminal/success).
	require.True(t, waitForSettlingSignalTypePrefix(t, h, a.ID, "terminal/error/", 30*time.Second),
		"a should record settling_signal_type=terminal/error/<class>")
	require.True(t, waitForSettlingSignalType(t, h, c.ID, "terminal/success", 30*time.Second),
		"c should record settling_signal_type=terminal/success")

	// B should land in failed via template_resolution_failed → give_up.
	require.True(t, h.WaitForNodeState(bNode.ID, cascade.NodeStateFailed, 30*time.Second),
		"b should land in failed via template_resolution_failed → give_up")

	var bRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, bNode.ID, tx)
		bRow = r
		return err
	}))
	require.NotNil(t, bRow.SettlingSignalType,
		"b should have a settling_signal_type after give_up")
	require.Contains(t, *bRow.SettlingSignalType, "terminal/error/",
		"b's give_up should record a terminal/error/<class> settling_signal_type")
}
