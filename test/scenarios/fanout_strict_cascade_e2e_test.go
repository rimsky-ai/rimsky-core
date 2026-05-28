// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// fanout_strict_cascade_e2e — pins the cross-scope cascade bridge from
// parent settlement under strict aggregation per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
//
// Under strict aggregation, the fan-out parent only settles when ALL
// children resolve. The parent's settlement goes through the
// aggregation walker (code:runtime/state_propagation.go::walkUpwards),
// NOT through applyTerminalComplete on the parent. Without the cross-
// scope cascade bridge in PropagateIfChildAfterTerminal, the parent's
// downstream main-graph subscribers would never receive the cascade
// because the walker doesn't fire cascadeSubscribersStaleInTx for the
// parent.
//
// This test:
//
//  1. Deploys a fan-out parent with strict aggregation + 3 partitions.
//  2. Each partition child returns Success.
//  3. Asserts the downstream main-graph subscriber receives the
//     cascade and reaches state=fresh — which only happens if the
//     bridge in PropagateIfChildAfterTerminal fires
//     cascadeSubscribersStaleInTx for the parent on settlement.
//
// The complementary F1 (`fanout_success_cascade_e2e`) test uses
// best_effort aggregation; the parent settles on the FIRST child's
// terminal via the standard applyTerminal path, which naturally fires
// the cascade — that's the incidental path. This test pins the
// architectural path.
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
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestFanOutStrictCascadeE2E(t *testing.T) {
	t.Parallel()

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

	// All children + downstream succeed cleanly. Strict aggregation
	// requires all-children-resolved for the parent to settle to fresh.
	h.Stub.WhenType("fan-parent").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("downstream").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fan-strict-cascade", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
						// Strict aggregation — parent only settles when
						// ALL children resolve. Pins the bridge path:
						// parent settlement via aggregation walker MUST
						// fire cascadeSubscribersStaleInTx for the
						// parent's downstream subscribers.
						ErrorPolicy: tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindStrict},
					},
				},
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream", Executor: "stub",
					Subscribes: []tmplspec.SubscriptionEntry{{Node: "fan-parent", Type: "terminal/*"}},
				},
				openAttrs,
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-fanout-strict-cascade", map[string]any{})

	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")
	downstreamNode := h.FindNode(iid, "downstream")
	require.NotNil(t, downstreamNode, "downstream node missing")

	// Wait for all three partition children to settle to state=fresh.
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
		"all three partition children should reach state=fresh")

	// Pin: under STRICT aggregation, parent settlement goes through
	// walkUpwards (not applyTerminal on the parent), so the cascade
	// bridge in PropagateIfChildAfterTerminal MUST fire to wake the
	// downstream subscriber. If the bridge is missing, downstream
	// stays stale forever and this fails on timeout.
	require.True(t,
		h.WaitForNodeState(downstreamNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"downstream must reach fresh via cross-scope cascade bridge "+
			"(PropagateIfChildAfterTerminal → cascadeSubscribersStaleInTx for the parent)")
}
