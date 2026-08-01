// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTemplateFanOutDelegation_PerPartitionSubgraphAggregates(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"fanout-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	h.Stub.WhenType("fan-caller").Success(map[string]any{"entered": true}, true, "entered")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"entered": map[string]any{"type": "boolean", "readOnly": true},
			"done":    map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-fan-out-delegation", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{
							Type:     "fan-caller",
							Delegate: "worker",
							FanOut: &tmplspec.FanOutSpec{
								Claim:            "data",
								PartitionRequest: `{"partition_keys":["a","b","c"]}`,
								ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindStrict},
							},
						},
						openAttrs,
						scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
					),
				},
			},
			{
				Name:  "worker",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-entry", Executor: "stub"},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
				},
			},
		},
	})

	iid := h.CreateInstance(tid, "ck-story-fan-out-delegation", map[string]any{})

	callerNode := h.FindNode(iid, "fan-caller")
	require.NotNil(t, callerNode, "fan-caller node missing")
	exitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, exitNode, "inner-exit node missing")

	require.Eventually(t, func() bool {
		var subClaims int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_claim_handles
			 WHERE parent_claim_handle_id IS NOT NULL
			   AND holder_node_id = $1
		`, []any{callerNode.ID}, &subClaims)
		return subClaims == 3
	}, 60*time.Second, 50*time.Millisecond,
		"supervisor must materialize three sub-claim rows from SplitScope's three sub-scopes even though "+
			"the fan-out node also delegates")

	require.Eventually(t, func() bool {
		var partitionScopes int
		h.QueryRowSQL(`
			SELECT COUNT(DISTINCT worker_scope.id)
			  FROM rimsky_run_scopes worker_scope
			  JOIN rimsky_run_scopes partition_scope ON partition_scope.id = worker_scope.parent_run_scope_id
			 WHERE worker_scope.instance_id = $1
			   AND worker_scope.graph_name = 'worker'
			   AND partition_scope.partition_key <> ''
		`, []any{iid}, &partitionScopes)
		return partitionScopes == 3
	}, 60*time.Second, 50*time.Millisecond,
		"each fan-out partition clone must open its own nested sub-graph RunScope (graph_name='worker') "+
			"parented under its own partition's RunScope — the delegated sub-graph must run once per "+
			"partition, not once total")

	require.Eventually(t, func() bool {
		var exitRuns int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.node_id = $1
			   AND rs.graph_name = 'worker'
			   AND r.state = 'fresh'
		`, []any{exitNode.ID}, &exitRuns)
		return exitRuns == 3
	}, 90*time.Second, 50*time.Millisecond,
		"the sub-graph exit must reach terminal success once per partition (three partition clones)")

	h.WaitForNodeState(callerNode.ID, cascade.NodeStateFresh)

	require.Eventually(t, func() bool {
		var settledClaims int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_claim_handles
			 WHERE parent_claim_handle_id IS NOT NULL
			   AND holder_node_id = $1
			   AND state = 'committed'
		`, []any{callerNode.ID}, &settledClaims)
		return settledClaims == 3
	}, 60*time.Second, 50*time.Millisecond,
		"each partition's fan-out sub-claim must commit once its own delegated sub-graph settles — "+
			"the claim must not be left dangling 'active' because the caller absorbed a delegate entry")

	var parentClaimState string
	h.QueryRowSQL(`
		SELECT ch.state
		  FROM rimsky_claim_handles ch
		 WHERE ch.holder_node_id = $1
		   AND ch.parent_claim_handle_id IS NULL
		   AND EXISTS (
		         SELECT 1 FROM rimsky_claim_handles child
		          WHERE child.parent_claim_handle_id = ch.id
		       )
	`, []any{callerNode.ID}, &parentClaimState)
	require.Equal(t, "committed", parentClaimState,
		"the fan-out parent claim must aggregate to committed once every partition's delegated sub-graph settles")

	require.Eventually(t, func() bool {
		var dangling int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_claim_handles
			 WHERE holder_node_id = $1
			   AND state = 'active'
		`, []any{callerNode.ID}, &dangling)
		return dangling == 0
	}, 30*time.Second, 50*time.Millisecond,
		"no claim acquired by the fan-out-and-delegate node may be left dangling 'active' once the "+
			"instance-visible outcome has settled")
}
