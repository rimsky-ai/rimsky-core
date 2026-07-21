// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: attribute-carry-forward
package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAttributeCarryForwardWithinRunScopeThenSubgraphSeesSchemaDefault(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("delegate").Success(map[string]any{}, true, "sub-entry")
	h.Stub.WhenType("sub_final").Success(map[string]any{}, true, "sub-final")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attribute-carry-forward", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{
							Type:     "counter",
							Executor: loop_counter.ExecutorAlias,
							Subscribes: []tmplspec.SubscriptionEntry{
								{
									Node:                 "counter",
									Type:                 "terminal/success",
									When:                 `"loop" in payload.tags`,
									ForceUpstreamRefresh: tmplspec.BoolPtr(false),
								},
							},
						},
						scenario.WithAttributes(map[string]any{
							"type": "object",
							"properties": map[string]any{
								"max": map[string]any{"type": "integer", "default": 3},
							},
						}),
					),
					{
						Type:     "delegate",
						Delegate: "subworker",
						Subscribes: []tmplspec.SubscriptionEntry{
							{
								Node:                 "counter",
								Type:                 "terminal/success",
								When:                 `"done" in payload.tags`,
								ForceUpstreamRefresh: tmplspec.BoolPtr(false),
							},
						},
					},
				},
			},
			{
				Name:  "subworker",
				Entry: "sub_entry",
				Exit:  "sub_final",
				Nodes: []node.TemplateNodeDef{
					{
						Type:     "sub_entry",
						Executor: "stub",
					},
					scenario.MakeNode(
						node.TemplateNodeDef{
							Type:     "sub_counter",
							Executor: loop_counter.ExecutorAlias,
							Subscribes: []tmplspec.SubscriptionEntry{
								{
									Node: "sub_entry", Type: "terminal/success",
									ForceUpstreamRefresh: tmplspec.BoolPtr(false),
								},
							},
						},
						scenario.WithAttributes(map[string]any{
							"type": "object",
							"properties": map[string]any{
								"max": map[string]any{"type": "integer", "default": 3},
							},
						}),
					),
					{
						Type:     "sub_final",
						Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{
							{
								Node: "sub_counter", Type: "terminal/success",
								ForceUpstreamRefresh: tmplspec.BoolPtr(false),
							},
						},
					},
				},
			},
		},
	})

	iid := h.CreateInstance(tid, "ck-attribute-carry-forward", map[string]any{})
	counterNode := h.FindNode(iid, "counter")
	require.NotNil(t, counterNode, "counter missing")
	subCounterNode := h.FindNode(iid, "sub_counter")
	require.NotNil(t, subCounterNode, "sub_counter missing")
	subFinalNode := h.FindNode(iid, "sub_final")
	require.NotNil(t, subFinalNode, "sub_final missing")

	h.WaitForNodeState(counterNode.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(subCounterNode.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(subFinalNode.ID, cascade.NodeStateFresh)

	var mainCounts []int
	h.QuerySQL(`
		SELECT (payload->'attributes_delta'->>'count')::int
		  FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		 ORDER BY id`,
		[]any{counterNode.ID},
		func(scan func(...any) error) error {
			var c int
			if err := scan(&c); err != nil {
				return err
			}
			mainCounts = append(mainCounts, c)
			return nil
		})
	require.Equal(t, []int{1, 2, 3}, mainCounts,
		"counter must advance step by step across three same-RunScope dispatches "+
			"via writeback plus carry-forward (dispatch N sees dispatch N-1's count)")

	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
	mainAttrs := latestAttrRow(h, counterNode.ID, mainScopeID)
	require.NotNil(t, mainAttrs, "counter must have a node_attributes row in the main RunScope")
	require.Equal(t, float64(3), mainAttrs.Data["count"],
		"counter's resolved attribute bag in the main RunScope must carry the third dispatch's count")

	var subScopeID shared.UUID
	h.QueryRowSQL(`
		SELECT id FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND graph_name = 'subworker' AND partition_key = ''
		 ORDER BY created_at DESC LIMIT 1`,
		[]any{iid}, &subScopeID)

	var subCounts []int
	h.QuerySQL(`
		SELECT (payload->'attributes_delta'->>'count')::int
		  FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		 ORDER BY id`,
		[]any{subCounterNode.ID},
		func(scan func(...any) error) error {
			var c int
			if err := scan(&c); err != nil {
				return err
			}
			subCounts = append(subCounts, c)
			return nil
		})
	require.Equal(t, []int{1}, subCounts,
		"sub_counter's first dispatch in its fresh sub-graph RunScope must count from the "+
			"schema default (0), not inherit the main RunScope's prior count=3 writeback")

	subAttrs := latestAttrRow(h, subCounterNode.ID, subScopeID)
	require.NotNil(t, subAttrs, "sub_counter must have a node_attributes row in the sub-graph RunScope")
	require.Equal(t, float64(1), subAttrs.Data["count"],
		"sub_counter's resolved attribute bag in the fresh sub-graph RunScope must reflect "+
			"the schema default plus one dispatch, not any carried-forward main-scope value")
}
