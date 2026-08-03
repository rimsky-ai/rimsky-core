// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	holds := [3]chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	builder := h.Stub.WhenType("fan-parent").
		Success(map[string]any{"ok": true}, true, "ok").
		HoldUntil(holds[0])
	for i := 1; i < len(holds); i++ {
		builder = builder.Then().
			Success(map[string]any{"ok": true}, true, "ok").
			HoldUntil(holds[i])
	}

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
		"all three partition children must be in flight at once: every dispatch is held open until this "+
			"test releases it, so three work_started events can only coexist if the three were dispatched "+
			"in parallel — serialized dispatch stalls on the first hold and never reaches the third")

	terminalChildren := func() int {
		var n int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.state IN ('fresh','failed')
			   AND rs.instance_id = $1
			   AND rs.partition_key <> ''
			   AND r.node_id = $2
		`, []any{iid, parentNode.ID}, &n)
		return n
	}

	requireParentNotSettled := func(afterReleases int) {
		parentState := parentNodeState(t, h, parentNode.ID)
		require.NotEqual(t, cascade.NodeStateFresh, parentState,
			"parent fan-out node settled to fresh after only %d of 3 partition children were released — "+
				"parent must wait for ALL sub-claims to resolve before settling",
			afterReleases)
		require.NotEqual(t, cascade.NodeStateFailed, parentState,
			"parent fan-out node settled to failed after only %d of 3 partition children were released",
			afterReleases)
	}

	for i, hold := range holds[:len(holds)-1] {
		close(hold)
		require.Eventually(t, func() bool { return terminalChildren() == i+1 },
			60*time.Second, 25*time.Millisecond,
			"released partition child %d never reached a terminal run state", i+1)
		requireParentNotSettled(i + 1)
	}
	close(holds[len(holds)-1])

	h.WaitForNodeState(parentNode.ID, cascade.NodeStateFresh)
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

	h.WaitForNodeState(parentNode.ID, cascade.NodeStateFailed)

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
