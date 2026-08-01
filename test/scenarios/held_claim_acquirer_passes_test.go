// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestHeldClaimAcquirerPasses(t *testing.T) {
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
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "should-not-run")
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-claim-acquirer-passes", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "acquirer",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Action: "pass",
						},
					},
				},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "@queue", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-acq-passes", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	waitForSettlingSignalTypePrefix(t, h, acq.ID, "terminal/error/")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		require.Empty(t, h.Stub.Observed(),
			"executor must not be invoked when the acquirer passes on Unavailable")
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			ri, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, inh.ID, tx)
			if err != nil {
				return err
			}
			if ri != nil {
				require.Equal(t, cascade.NodeStateFresh, ri.State,
					"inheritor should remain fresh — pass should not cascade to it")
			}
			return nil
		}))
		time.Sleep(50 * time.Millisecond)
	}

	var acqLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, acq.ID, tx)
		acqLatest = ra
		return err
	}))
	require.NotNil(t, acqLatest)
	require.Equal(t, cascade.NodeStateFresh, acqLatest.State,
		"acquirer should be fresh after pass")

	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Equal(t, 0, lhCount,
		"no claim_handle rows should exist when the producer returned Unavailable")

	var chCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_holders ch
		   JOIN rimsky_node_runs r ON r.id = ch.holder_run_id
		   JOIN rimsky_nodes n ON n.id = r.node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&chCount))
	require.Equal(t, 0, chCount,
		"no claim_holders rows should exist for an unacquired claim")
}
