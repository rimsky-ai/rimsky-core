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

// @story: held-abandon-cascades-abandoned
// @decision: terminal-error-abandoned-as-error-class
func TestHeldAbandonCascadesAbandoned(t *testing.T) {
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
	h.Stub.WhenType("inheritor").Error("forced", map[string]any{"why": "rolling-back"})
	h.Stub.WhenType("observer").Success(map[string]any{}, true, "observer-saw-abandon")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-abandon-cascades-abandoned", Version: "1",
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
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/forced": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}),
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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/error/abandoned", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-abandon-cascades-abandoned", map[string]any{})
	acquirer := h.FindNode(iid, "acquirer")
	inheritor := h.FindNode(iid, "inheritor")
	observer := h.FindNode(iid, "observer")
	require.NotNil(t, acquirer)
	require.NotNil(t, inheritor)
	require.NotNil(t, observer)

	require.True(t, h.WaitForNodeState(inheritor.ID, cascade.NodeStateFailed, 30*time.Second),
		"inheritor should fail (give_up on stub/forced)")
	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFailed, 30*time.Second),
		"acquirer should transition held → failed via auto-terminal abandon")

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateAbandoned, true)

	var acquirerLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, acquirer.ID)
		acquirerLatest = r
		return err
	}))
	require.NotNil(t, acquirerLatest)
	require.NotNil(t, acquirerLatest.SettlingSignalType)
	require.Equal(t, "terminal/error/abandoned", *acquirerLatest.SettlingSignalType,
		"acquirer's settling_signal_type must be terminal/error/abandoned (the new cascade-firable abandon signal)")

	require.True(t, h.WaitForNodeState(observer.ID, cascade.NodeStateFresh, 30*time.Second),
		"non-member observer subscribed to terminal/error/abandoned should dispatch after held work rolls back (deferred cascade path)")
}
