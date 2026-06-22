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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTemplateFanOut_HappyPath_AllSuccess(t *testing.T) {
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

	const leafDelay = 600 * time.Millisecond

	h.Stub.WhenType("fan-parent").
		Success(map[string]any{"ok": true}, true, "ok").
		Delay(leafDelay)

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-fan-out-happy", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
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
	})

	iid := h.CreateInstance(tid, "ck-story-fan-out-happy", map[string]any{})

	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")

	require.Eventually(t, func() bool {
		var subClaims int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_claim_handles
			 WHERE parent_claim_handle_id IS NOT NULL
			   AND holder_node_id = $1
		`, []any{parentNode.ID}, &subClaims)
		return subClaims == 3
	}, 60*time.Second, 50*time.Millisecond,
		"supervisor must materialize three sub-claim rows from SplitScope's three sub-scopes")

	require.Eventually(t, func() bool {
		var ws int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_events
			 WHERE node_id = $1 AND kind = 'work_started'
		`, []any{parentNode.ID}, &ws)
		return ws >= 3
	}, 60*time.Second, 25*time.Millisecond,
		"each of the three partition children must emit a work_started event "+
			"(dispatch reached the runner's post-acquisition audit tx)")

	var spreadMs int64
	h.QueryRowSQL(`
		SELECT EXTRACT(EPOCH FROM (MAX(occurred_at) - MIN(occurred_at)))::bigint * 1000
		  FROM (
		    SELECT occurred_at FROM rimsky_events
		     WHERE node_id = $1 AND kind = 'work_started'
		     ORDER BY occurred_at ASC
		     LIMIT 3
		  ) sub
	`, []any{parentNode.ID}, &spreadMs)
	require.Less(t, spreadMs, leafDelay.Milliseconds(),
		"work_started events for the three partition runs must be concurrent — "+
			"observed spread %dms ≥ per-leaf delay %dms suggests serialized dispatch "+
			"(fan-out children must be dispatched in parallel, not one after another)",
		spreadMs, leafDelay.Milliseconds())

	verifiedParentHeld := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var freshChildren, terminalChildren int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.state = 'fresh'
			   AND rs.instance_id = $1
			   AND rs.partition_key <> ''
			   AND r.node_id = $2
		`, []any{iid, parentNode.ID}, &freshChildren)
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.state IN ('fresh','failed')
			   AND rs.instance_id = $1
			   AND rs.partition_key <> ''
			   AND r.node_id = $2
		`, []any{iid, parentNode.ID}, &terminalChildren)
		if terminalChildren >= 1 && terminalChildren < 3 {
			parentState := parentNodeState(t, h, parentNode.ID)
			require.NotEqual(t, cascade.NodeStateFresh, parentState,
				"parent fan-out node settled to fresh while only %d of 3 partition children had terminated — "+
					"parent must wait for ALL sub-claims to resolve before settling",
				terminalChildren)
			require.NotEqual(t, cascade.NodeStateFailed, parentState,
				"parent fan-out node settled to failed while only %d of 3 partition children had terminated",
				terminalChildren)
			verifiedParentHeld = true
		}
		if freshChildren >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, verifiedParentHeld,
		"never observed the parent in-flight with some-but-not-all partition children terminated — "+
			"the leaf delay (%s) should have created a wide enough natural window for the 20ms poll "+
			"to catch the moment; if this fails the aggregation path may be settling the parent "+
			"out-of-order relative to the children",
		leafDelay)

	require.True(t,
		h.WaitForNodeState(parentNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"parent fan-out node must reach NodeStateFresh once all three sub-claim children Succeed under strict aggregation")
}

func TestTemplateFanOut_AbandonPropagatesToParentError(t *testing.T) {
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

	h.Stub.WhenType("fan-parent").Error("fanout_doom", map[string]any{"why": "leaf abandoned"})

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-fan-out-abandon-propagates", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
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
	})

	iid := h.CreateInstance(tid, "ck-story-fan-out-abandon", map[string]any{})

	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")

	require.Eventually(t, func() bool {
		var subClaims int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_claim_handles
			 WHERE parent_claim_handle_id IS NOT NULL
			   AND holder_node_id = $1
		`, []any{parentNode.ID}, &subClaims)
		return subClaims == 3
	}, 60*time.Second, 50*time.Millisecond,
		"sub-claim materialization must precede leaf execution — three sub-claim rows expected")

	require.True(t,
		h.WaitForNodeState(parentNode.ID, cascade.NodeStateFailed, 90*time.Second),
		"parent fan-out node must reach NodeStateFailed once any sub-claim is Abandon'd under strict aggregation "+
			"(strict_failed projection from runtime/run_tree.go::aggregateStrict)")

	var parentSettlingSig string
	var lastReadErr error
	sigDeadline := time.Now().Add(30 * time.Second)
	for {
		var sig string
		lastReadErr = h.Pool.QueryRow(h.Ctx, `
			SELECT COALESCE(r.settling_signal_type, '')
			  FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.node_id = $1
			   AND rs.partition_key = ''
			 ORDER BY COALESCE(r.active_terminal_at, r.enqueued_at) DESC
			 LIMIT 1
		`, parentNode.ID).Scan(&sig)
		if lastReadErr == nil {
			parentSettlingSig = sig
		}
		if parentSettlingSig == "terminal/error/aggregate/strict_failed" {
			break
		}
		if time.Now().After(sigDeadline) {
			t.Fatalf("parent main-scope run's settling_signal_type must carry the strict_failed aggregate signal "+
				"(aggregateStrict's projection from sub-claim Abandon → parent Failed); last read: %q (last read error: %v)",
				parentSettlingSig, lastReadErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func parentNodeState(t *testing.T, h *scenario.Harness, nodeID shared.UUID) cascade.NodeState {
	t.Helper()
	var state string
	h.QueryRowSQL(`
		SELECT COALESCE(r.state, 'fresh')
		  FROM rimsky_nodes n
		  LEFT JOIN LATERAL (
		    SELECT state, active_terminal_at, enqueued_at
		      FROM rimsky_node_runs
		     WHERE node_id = n.id
		     ORDER BY CASE WHEN state IN ('pending','stale','running','held','parked') THEN 0 ELSE 1 END,
		              COALESCE(active_terminal_at, enqueued_at) DESC
		     LIMIT 1
		  ) r ON TRUE
		 WHERE n.id = $1
	`, []any{nodeID}, &state)
	return cascade.NodeState(state)
}
