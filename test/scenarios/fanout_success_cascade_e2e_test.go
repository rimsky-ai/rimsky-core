// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// F1 must-pass scenario — fanout_success_cascade_e2e.
//
// End-to-end coverage of the happy-path fan-out lifecycle under the
// RunScope-first reshape per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Test coverage matrix / F1":
//
//   - Fan-out parent emits three partitions via stub-store SplitScope.
//   - Each child executes to a Success terminal.
//   - Each partition lives in its own fanout_partition RunScope.
//   - Downstream main-graph subscriber receives the cascade.
//
// Pins three load-bearing properties of the reshape:
//
//  1. Each partition's children are dispatched (three fan-parent
//     ExecuteRequests observed via the stub).
//  2. Three fanout_partition RunScopes are created at SplitScope sub-
//     claim acquisition per
//     code:runtime/runner_subclaim.go::AcquireSubClaims, each rooted
//     under the fan-out parent's main-scope run via parent_run_id.
//  3. All partition-children's leaf-runs reach state=fresh, and the
//     downstream subscriber's cascade fires (downstream run reaches
//     state=fresh via the cascade walker per
//     code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx).
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/control/config"
	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	tmplspec "github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
	"github.com/fallguyconsulting/rimsky/protocols/claimproducer"
	stubstore "github.com/fallguyconsulting/rimsky/stores/stub/store"
	stubfixture "github.com/fallguyconsulting/rimsky/stores/stub/testfixture"
)

func TestFanOutSuccessCascadeE2E(t *testing.T) {
	t.Parallel()

	// Remote stub store. The fixture's ClaimProducer surface advertises
	// SupportsSplitScope=true and decodes
	// {"partition_keys":[...]} into one SubScopeDescriptor per key.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"fanout-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	// Per-child + downstream scripts. Each script returns Success so the
	// parent's aggregation settles cleanly. best_effort tolerates any
	// per-child outcome — this scenario asserts dispatch + cascade
	// shape, not aggregation policy semantics.
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
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
			// Downstream main-graph subscriber. The subscription edge
			// targets "fan-parent" so the cascade walker reaches it
			// when partition children settle.
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream", Executor: "stub",
					Subscribes: []tmplspec.SubscriptionEntry{{Node: "fan-parent", Type: "terminal/*"}},
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

	// Wait for all three partition children to dispatch through the stub.
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

	// Three fanout_partition RunScopes are created (one per partition
	// key), each rooted under the parent run via parent_run_id.
	var partitionCount int
	h.QueryRowSQL(`
		SELECT COUNT(*)
		  FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND partition_key <> ''
	`, []any{iid}, &partitionCount)
	require.Equal(t, 3, partitionCount,
		"three fanout_partition RunScopes should be created by AcquireSubClaims")

	// Each partition's leaf-run reaches state=fresh on Success terminal.
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

	// The downstream main-graph subscriber receives the cascade and
	// reaches state=fresh after its own dispatch. With best_effort
	// aggregation the downstream may receive the cascade from any
	// partition's settlement; that's enough to pin cross-RunScope
	// cascade plumbing.
	require.True(t,
		h.WaitForNodeState(downstreamNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"downstream must reach fresh via the cascade walker")

	// Pin Phase G carryover #2: the parent claim handle's
	// expected_children_count must reach 3 after AcquireSubClaims
	// commits — that count is what the recursive aggregation walker
	// uses to detect "all children resolved" via
	// committed+abandoned == expected. Without it the partition-scope
	// closure path can't fire from the parent settlement side.
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
