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
	inheritorGate := make(chan struct{})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "inheritor-done").HoldUntil(inheritorGate)
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

	h.WaitForNodeState(acquirer.ID, cascade.NodeStateHeld)

	var observerRunsAtHeld int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{observer.ID}, &observerRunsAtHeld)
	require.Zero(t, observerRunsAtHeld,
		"B must still be gated at the moment A settles held: the held-terminal cascade emit is filtered "+
			"to subgraph members only and creates receiver runs atomically with A's held transition, so "+
			"any observer node-run existing while A is held means the cascade fired to a non-member "+
			"before auto-terminal commit; got %d run(s)", observerRunsAtHeld)

	close(inheritorGate)

	h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(inheritor.ID, cascade.NodeStateFresh)

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)

	var acquirerLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, acquirer.ID, tx)
		acquirerLatest = r
		return err
	}))
	require.NotNil(t, acquirerLatest)
	require.NotNil(t, acquirerLatest.SettlingSignalType)
	require.Equal(t, "terminal/success", *acquirerLatest.SettlingSignalType,
		"acquirer's settling_signal_type must be terminal/success (the auto-terminal commit signal)")

	h.WaitForNodeState(observer.ID, cascade.NodeStateFresh)
	h.WaitForEventCount(observer.ID, "terminal/success", 1)

	var acquirerSuccessTimes []time.Time
	h.QuerySQL(`
		SELECT occurred_at FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		 ORDER BY occurred_at ASC`,
		[]any{acquirer.ID},
		func(scan func(...any) error) error {
			var ts time.Time
			if err := scan(&ts); err != nil {
				return err
			}
			acquirerSuccessTimes = append(acquirerSuccessTimes, ts)
			return nil
		})
	require.Len(t, acquirerSuccessTimes, 1,
		"acquirer audits terminal/success exactly once: the held moment fires a member-filtered CASCADE "+
			"but writes NO audit event (a held node-run's running-to-held transition emits no terminal "+
			"signal), so the only audited terminal/success is the auto-terminal commit moment; got %d",
		len(acquirerSuccessTimes))

	var observerSuccessTime time.Time
	h.QueryRowSQL(`
		SELECT occurred_at FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		 ORDER BY occurred_at ASC LIMIT 1`,
		[]any{observer.ID}, &observerSuccessTime)

	require.False(t, observerSuccessTime.Before(acquirerSuccessTimes[0]),
		"B must not dispatch before A's auto-terminal commit: B's terminal/success occurred at %s, "+
			"which must not be earlier than A's commit-moment terminal/success at %s — an "+
			"earlier B settlement would mean B fired on A's held terminal instead of waiting for commit",
		observerSuccessTime, acquirerSuccessTimes[0])

	var inheritorRuns int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{inheritor.ID}, &inheritorRuns)
	require.Equal(t, 1, inheritorRuns,
		"inheritor (a subgraph member, and itself subscribed to acquirer's terminal/success) must dispatch "+
			"exactly once from the held-moment member-filtered cascade: the commit-moment cascade is filtered "+
			"to non-members only, so a member must never see it a second time; got %d run(s), which would mean "+
			"the non-member filter leaked back to members and double-fired inheritor", inheritorRuns)
}
