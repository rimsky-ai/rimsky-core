// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHoldsOnlyAutoTerminal(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("coholder").Success(map[string]any{}, true, "co-held")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "holds-only-auto-terminal", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("content", "/region-held", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "coholder",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-holds-only-auto-terminal", map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	coholder := h.FindNode(iid, "coholder")
	require.NotNil(t, acquirer)
	require.NotNil(t, coholder)

	h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(coholder.ID, cascade.NodeStateFresh)

	deadline := time.Now().Add(5 * time.Second)
	var commitCount, abandonCount int
	for time.Now().Before(deadline) {
		commitCount, abandonCount = 0, 0
		for _, c := range sub.Calls() {
			switch c.Verb {
			case "commit":
				commitCount++
			case "abandon":
				abandonCount++
			}
		}
		if commitCount >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, 1, commitCount,
		"a holds:-only claim must drive exactly one aggregate Commit over the co-holder set")
	require.Equal(t, 0, abandonCount,
		"aggregate-completed (all-success) must NOT route to Abandon")

	var activeHolderCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_claim_holders ch
		   JOIN rimsky_node_runs r ON r.id = ch.holder_run_id
		   JOIN rimsky_nodes n ON n.id = r.node_id
		  WHERE n.instance_id = $1 AND ch.state = 'active'`,
		[]any{iid}, &activeHolderCount,
	)
	require.Equal(t, 0, activeHolderCount,
		"no rimsky_claim_holders row may remain 'active' after the held auto-terminal fires — "+
			"a stranded co-holder row means the holds:-only claim was never recognized as held "+
			"and the documented aggregate Commit never resolved the co-holder set")

	var committedHandleCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'committed'`,
		[]any{iid}, &committedHandleCount,
	)
	require.Greater(t, committedHandleCount, 0,
		"the acquirer's claim handle must reach state='committed' after the held auto-terminal Commit")

	var activeHandleCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'active'`,
		[]any{iid}, &activeHandleCount,
	)
	require.Equal(t, 0, activeHandleCount,
		"no claim handle may remain 'active' after both nodes settle")
}
