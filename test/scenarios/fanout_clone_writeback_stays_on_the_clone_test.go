// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: child-execution
// @decision: fanout-attribute-merge-rejected
func TestFanOutCloneWritebacksNeverAggregateOntoTheParent(t *testing.T) {
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

	h.Stub.WhenType("fan-parent").
		Success(map[string]any{"clone_tag": "partition-1"}, true, "clone-1").
		Then().Success(map[string]any{"clone_tag": "partition-2"}, true, "clone-2").
		Then().Success(map[string]any{"clone_tag": "partition-3"}, true, "clone-3")

	cloneAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"clone_tag": map[string]any{"type": "string", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fanout-clone-writeback", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindBestEffort},
					},
				},
				cloneAttrs,
				scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-fanout-clone-writeback", map[string]any{})
	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode)

	awaited.Until(t, "all three partition clones to write back their own tag", func() bool {
		var written int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_node_attributes a
			  JOIN rimsky_node_runs r ON r.id = a.node_run_id
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.node_id = $1 AND rs.partition_key <> '' AND a.data ? 'clone_tag'
		`, []any{parentNode.ID}, &written)
		return written >= 3
	})
	h.WaitForSchedulerQuiescence()

	var distinctCloneTags int
	h.QueryRowSQL(`
		SELECT COUNT(DISTINCT a.data->>'clone_tag')
		  FROM rimsky_node_attributes a
		  JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
		 WHERE r.node_id = $1 AND rs.partition_key <> ''
	`, []any{parentNode.ID}, &distinctCloneTags)
	require.Equal(t, 3, distinctCloneTags,
		"each clone wrote a tag of its own, so a merge onto the parent would have to pick a winner")

	var parentRunsCarryingACloneTag int
	h.QueryRowSQL(`
		SELECT COUNT(*)
		  FROM rimsky_node_attributes a
		  JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
		 WHERE r.node_id = $1 AND rs.partition_key = '' AND a.data ? 'clone_tag'
	`, []any{parentNode.ID}, &parentRunsCarryingACloneTag)
	require.Equal(t, 0, parentRunsCarryingACloneTag,
		"a clone's attribute writeback never merges into the fan-out parent's attribute bag")
}
