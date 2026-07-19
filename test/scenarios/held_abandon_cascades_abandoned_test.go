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
	inheritorGate := make(chan struct{})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("inheritor").Error("forced", map[string]any{"why": "rolling-back"}).HoldUntil(inheritorGate)
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
							Action: "give_up",
						},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", ForceUpstreamRefresh: spec.BoolPtr(false)}),
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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/error/abandoned", ForceUpstreamRefresh: spec.BoolPtr(false)}),
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

	h.WaitForNodeState(acquirer.ID, cascade.NodeStateHeld)

	var observerRunsAtHeld int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{observer.ID}, &observerRunsAtHeld)
	require.Zero(t, observerRunsAtHeld,
		"B must NOT dispatch on A's held terminal: the held-terminal cascade emit is filtered to "+
			"subgraph members only and creates receiver runs atomically with A's held transition, so "+
			"any observer node-run existing while A is held means the cascade leaked to a non-member "+
			"before auto-terminal abandon; got %d run(s)", observerRunsAtHeld)

	close(inheritorGate)

	h.WaitForNodeState(inheritor.ID, cascade.NodeStateFailed)
	h.WaitForNodeState(acquirer.ID, cascade.NodeStateFailed)

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

	h.WaitForNodeState(observer.ID, cascade.NodeStateFresh)
	h.WaitForEventKind(observer.ID, "terminal/success")

	var observerLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, observer.ID)
		observerLatest = r
		return err
	}))
	require.NotNil(t, observerLatest)

	var waitSetRows int
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_wait_set
		 WHERE receiver_run_id = $1 AND sender_run_id = $2 AND topic_kind = 'terminal'`,
		[]any{observerLatest.NodeRunID, acquirerLatest.NodeRunID}, &waitSetRows)
	require.GreaterOrEqual(t, waitSetRows, 1,
		"B's wait-set must carry a row tying its dispatch to A's terminal signal (lineage: receiver_run=%s sender_run=%s)",
		observerLatest.NodeRunID, acquirerLatest.NodeRunID)

	var acquirerSignalTimes []time.Time
	var acquirerSignalKinds []string
	h.QuerySQL(`
		SELECT occurred_at, kind FROM rimsky_events
		 WHERE node_id = $1 AND kind IN ('terminal/success', 'terminal/error/abandoned')
		 ORDER BY occurred_at ASC`,
		[]any{acquirer.ID},
		func(scan func(...any) error) error {
			var ts time.Time
			var kind string
			if err := scan(&ts, &kind); err != nil {
				return err
			}
			acquirerSignalTimes = append(acquirerSignalTimes, ts)
			acquirerSignalKinds = append(acquirerSignalKinds, kind)
			return nil
		})
	require.Equal(t, []string{"terminal/success", "terminal/error/abandoned"}, acquirerSignalKinds,
		"acquirer must emit terminal/success (held moment, filtered to subgraph members only, so B never "+
			"sees it) then terminal/error/abandoned (auto-terminal abandon moment, unfiltered, so B sees "+
			"it) in that order; got %v", acquirerSignalKinds)

	var observerSuccessTime time.Time
	h.QueryRowSQL(`
		SELECT occurred_at FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		 ORDER BY occurred_at ASC LIMIT 1`,
		[]any{observer.ID}, &observerSuccessTime)

	require.False(t, observerSuccessTime.Before(acquirerSignalTimes[1]),
		"B must not dispatch before A's auto-terminal abandon: B's terminal/success occurred at %s, "+
			"which must not be earlier than A's terminal/error/abandoned at %s — an earlier B settlement "+
			"would mean B fired on A's held terminal instead of waiting for the abandon",
		observerSuccessTime, acquirerSignalTimes[1])

	var inheritorRuns int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{inheritor.ID}, &inheritorRuns)
	require.Equal(t, 1, inheritorRuns,
		"inheritor (a subgraph member, and itself subscribed to acquirer's terminal/*) must dispatch "+
			"exactly once from the held-moment member-filtered cascade: the abandon-moment cascade is filtered "+
			"to non-members only, so a member must never see it a second time; got %d run(s), which would mean "+
			"the non-member filter leaked back to members and double-fired inheritor", inheritorRuns)
}
