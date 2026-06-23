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
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFanOutSuccessCascadeE2E(t *testing.T) {
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

	h.Stub.WhenType("fan-parent").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("downstream").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fan-success-cascade", Version: "1",
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
				openAttrs,
				scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream", Executor: "stub",
					Subscribes: []tmplspec.SubscriptionEntry{{Node: "fan-parent", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
				},
				openAttrs,
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-fanout-success-cascade", map[string]any{})

	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")
	downstreamNode := h.FindNode(iid, "downstream")
	require.NotNil(t, downstreamNode, "downstream node missing")

	deadline := time.Now().Add(60 * time.Second)
	gotChildren := false
	for time.Now().Before(deadline) {
		count := 0
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "fan-parent" {
				count++
			}
		}
		if count >= 3 {
			gotChildren = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, gotChildren, "expected three fan-parent child dispatches via SplitScope")

	var partitionCount int
	h.QueryRowSQL(`
		SELECT COUNT(*)
		  FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND partition_key <> ''
	`, []any{iid}, &partitionCount)
	require.Equal(t, 3, partitionCount,
		"three fanout_partition RunScopes should be created by AcquireSubClaims")

	require.Eventually(t, func() bool {
		var freshRuns int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.state = 'fresh'
			   AND rs.instance_id = $1
			   AND rs.partition_key <> ''
			   AND r.node_id = $2
		`, []any{iid, parentNode.ID}, &freshRuns)
		return freshRuns >= 3
	}, 60*time.Second, 100*time.Millisecond,
		"all three partition children should reach state=fresh after Success terminal")

	require.True(t,
		h.WaitForNodeState(downstreamNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"downstream must reach fresh via the cascade walker")

	var parentExpected int
	h.QueryRowSQL(`
		SELECT COALESCE(MAX(expected_children_count), 0)
		  FROM rimsky_claim_handles lh
		  JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		 WHERE n.instance_id = $1
		   AND lh.parent_claim_handle_id IS NULL
	`, []any{iid}, &parentExpected)
	require.Equal(t, 3, parentExpected,
		"parent claim handle's expected_children_count must equal 3 after SplitScope acquires three sub-claims")
}
