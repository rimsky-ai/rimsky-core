// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: run-scope
// @concept: fan-out
package runtree

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

func startFanOutHarness(t *testing.T) *scenario.Harness {
	t.Helper()
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)
	return scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"tree-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
}

func fanOutTemplate(h *scenario.Harness, name string, policy tmplspec.AggregationPolicy) string {
	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})
	return h.DeployTemplate(node.TemplateSpec{
		Name: name, Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
						ErrorPolicy:      policy,
					},
				},
				openAttrs,
				scenario.WithClaimProducers(scenario.AliasedClaimRef("tree-store", "data", "rw", "data")),
			),
		},
	})
}

type persistedTreeShape struct {
	MainScopeID    string
	PartitionKeys  []string
	ParentRunIDs   map[string]string
	ChildStates    map[string]string
	ChildScopeIDs  map[string]string
	AllChildrenSet bool
}

func readPersistedTree(t *testing.T, h *scenario.Harness, iid any) persistedTreeShape {
	t.Helper()
	out := persistedTreeShape{
		ParentRunIDs:  map[string]string{},
		ChildStates:   map[string]string{},
		ChildScopeIDs: map[string]string{},
	}
	h.QueryRowSQL(`
		SELECT id::text FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND parent_run_scope_id IS NULL`,
		[]any{iid}, &out.MainScopeID)
	h.QuerySQL(`
		SELECT rs.id::text, rs.partition_key, COALESCE(rs.parent_run_id::text, ''),
		       COALESCE(r.state::text, '')
		  FROM rimsky_run_scopes rs
		  LEFT JOIN LATERAL (
			SELECT state FROM rimsky_node_runs
			 WHERE run_scope_id = rs.id
			 ORDER BY enqueued_at DESC LIMIT 1
		  ) r ON true
		 WHERE rs.instance_id = $1
		   AND rs.parent_run_scope_id IS NOT NULL
		 ORDER BY rs.partition_key`,
		[]any{iid}, func(scan func(...any) error) error {
			var scopeID, pk, parentRunID, state string
			if err := scan(&scopeID, &pk, &parentRunID, &state); err != nil {
				return err
			}
			out.PartitionKeys = append(out.PartitionKeys, pk)
			out.ParentRunIDs[pk] = parentRunID
			out.ChildStates[pk] = state
			out.ChildScopeIDs[pk] = scopeID
			return nil
		})
	return out
}

func waitForParentMainRunState(t *testing.T, h *scenario.Harness, nodeID any, mainScopeID string, want cascade.NodeState) string {
	t.Helper()
	for {
		var state, runID string
		h.QueryRowSQL(`
			SELECT COALESCE(state::text, ''), COALESCE(id::text, '')
			  FROM rimsky_node_runs
			 WHERE node_id = $1 AND run_scope_id = $2
			 ORDER BY enqueued_at DESC
			 LIMIT 1`,
			[]any{nodeID, mainScopeID}, &state, &runID)
		if state == string(want) {
			return runID
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRunTreePersistedShapeAndStrictAggregation_AllSuccess(t *testing.T) {
	t.Parallel()
	h := startFanOutHarness(t)
	h.Stub.WhenType("fan-parent").Success(map[string]any{"ok": true}, true, "ok")

	tid := fanOutTemplate(h, "run-tree-persisted-success",
		tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindStrict})
	iid := h.CreateInstance(tid, "ck-run-tree-success", map[string]any{})

	parent := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parent)

	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
	parentRunID := waitForParentMainRunState(t, h, parent.ID, mainScopeID.String(), cascade.NodeStateFresh)

	tree := readPersistedTree(t, h, iid)
	require.Equal(t, mainScopeID.String(), tree.MainScopeID,
		"the main scope must be the single parentless run-scope row")
	require.Equal(t, []string{"a", "b", "c"}, tree.PartitionKeys,
		"the scheduler must materialize exactly one child run-scope per partition key")
	for _, pk := range tree.PartitionKeys {
		require.Equal(t, parentRunID, tree.ParentRunIDs[pk],
			"child scope %q must point parent_run_id at the parent's main-scope run — the persisted tree edge Aggregate walks", pk)
		require.Equal(t, string(cascade.NodeStateFresh), tree.ChildStates[pk],
			"partition %q run must have settled fresh", pk)
	}

	var settlingSig string
	h.QueryRowSQL(`
		SELECT COALESCE(settling_signal_type, '') FROM rimsky_node_runs WHERE id = $1`,
		[]any{parentRunID}, &settlingSig)
	require.Equal(t, "terminal/success", settlingSig,
		"strict aggregation over three fresh children must settle the parent run with terminal/success")
}

func TestRunTreePersistedShapeAndStrictAggregation_ChildFailureFailsParent(t *testing.T) {
	t.Parallel()
	h := startFanOutHarness(t)
	h.Stub.WhenType("fan-parent").Error("partition_doom", map[string]any{"why": "leaf failed"})

	tid := fanOutTemplate(h, "run-tree-persisted-failure",
		tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindStrict})
	iid := h.CreateInstance(tid, "ck-run-tree-failure", map[string]any{})

	parent := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parent)

	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
	parentRunID := waitForParentMainRunState(t, h, parent.ID, mainScopeID.String(), cascade.NodeStateFailed)

	tree := readPersistedTree(t, h, iid)
	require.Equal(t, []string{"a", "b", "c"}, tree.PartitionKeys)
	failedChildren := 0
	for _, pk := range tree.PartitionKeys {
		require.Equal(t, parentRunID, tree.ParentRunIDs[pk],
			"child scope %q must link to the parent run even on the failure path", pk)
		if tree.ChildStates[pk] == string(cascade.NodeStateFailed) {
			failedChildren++
		}
	}
	require.GreaterOrEqual(t, failedChildren, 1,
		"at least one partition child must have persisted failed for strict aggregation to fail the parent")

	var settlingSig string
	h.QueryRowSQL(`
		SELECT COALESCE(settling_signal_type, '') FROM rimsky_node_runs WHERE id = $1`,
		[]any{parentRunID}, &settlingSig)
	require.Equal(t, "terminal/error/aggregate/strict_failed", settlingSig,
		"strict aggregation over a failed child must project the strict_failed aggregate signal onto the parent run")
}
