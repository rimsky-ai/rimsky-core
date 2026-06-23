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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: held-commit-cascades-success
// @decision: held-as-state-not-phase
func TestHeldCommitCascadesSuccess(t *testing.T) {
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
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "inheritor-done")
	h.Stub.WhenType("observer").Success(map[string]any{}, true, "observer-saw-commit")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-commit-cascades-success", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "/region", "rw", "schema")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"schema": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", ForceUpstreamRefresh: spec.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"held_addr": map[string]any{"type": "string", "source": "{{claim.schema.address}}"},
					},
					"required": []any{"held_addr"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "observer", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", ForceUpstreamRefresh: spec.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-commit-cascades-success", map[string]any{})
	acquirer := h.FindNode(iid, "acquirer")
	inheritor := h.FindNode(iid, "inheritor")
	observer := h.FindNode(iid, "observer")
	require.NotNil(t, acquirer)
	require.NotNil(t, inheritor)
	require.NotNil(t, observer)

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should commit and reach fresh after the inheritor completes")
	require.True(t, h.WaitForNodeState(inheritor.ID, cascade.NodeStateFresh, 30*time.Second),
		"inheritor should reach fresh after dispatching with the inherited claim")

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)

	var acquirerLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, acquirer.ID)
		acquirerLatest = r
		return err
	}))
	require.NotNil(t, acquirerLatest)
	require.NotNil(t, acquirerLatest.SettlingSignalType)
	require.Equal(t, "terminal/success", *acquirerLatest.SettlingSignalType,
		"acquirer's settling_signal_type must be terminal/success (the auto-terminal commit signal)")

	require.True(t, h.WaitForNodeState(observer.ID, cascade.NodeStateFresh, 30*time.Second),
		"non-member observer should dispatch and reach fresh AFTER held work commits (deferred cascade path)")
}
