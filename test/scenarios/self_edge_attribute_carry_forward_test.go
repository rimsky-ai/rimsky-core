// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSelfEdgeIntraFrameLoop_ReadOnlyAttributeCarriesForwardAcrossDispatches(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("loop-worker").
		Success(map[string]any{"session_marker": "tok-1", "turn": 1}, true, "turn1").
		Then().Success(map[string]any{"session_marker": "tok-2", "turn": 2}, true, "turn2").
		Then().Success(map[string]any{"session_marker": "tok-3", "turn": 3}, true, "turn3")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "self-edge-carry-forward", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "loop-worker",
					Executor: "stub",
					Subscribes: []tmplspec.SubscriptionEntry{
						{
							Node:                 "loop-worker",
							Type:                 "terminal/success",
							When:                 "payload.attributes_delta.turn < 3",
							ForceUpstreamRefresh: tmplspec.BoolPtr(false),
						},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_marker": map[string]any{
							"type": "string", "readOnly": true, "default": "",
						},
						"turn": map[string]any{
							"type": "integer", "readOnly": true, "default": 0,
						},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-self-edge-carry-forward", map[string]any{})

	worker := h.FindNode(iid, "loop-worker")
	require.NotNil(t, worker, "loop-worker node missing")

	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	observedForType := func(nodeType string) []map[string]any {
		var out []map[string]any
		for _, o := range h.Stub.Observed() {
			if o.NodeType == nodeType {
				out = append(out, o.Attributes)
			}
		}
		return out
	}
	obs := observedForType("loop-worker")
	require.Len(t, obs, 3,
		"self-edge loop must produce exactly 3 dispatches of loop-worker (initial + 2 self-triggered "+
			"re-dispatches gated by payload.attributes_delta.turn < 3); got %d", len(obs))

	require.Equal(t, "", obs[0]["session_marker"],
		"dispatch 1 must see session_marker at its schema default (\"\"); nothing has been committed yet")
	require.Equal(t, "tok-1", obs[1]["session_marker"],
		"dispatch 2 must see session_marker=%q, the readOnly value committed by dispatch 1's "+
			"attributes_delta -- this is the intra-RunScope carry-forward mechanism session_token "+
			"resume relies on", "tok-1")
	require.Equal(t, "tok-2", obs[2]["session_marker"],
		"dispatch 3 must see session_marker=%q, the readOnly value committed by dispatch 2's "+
			"attributes_delta", "tok-2")

	var runScopeIDs []shared.UUID
	h.QuerySQL(`
		SELECT run_scope_id FROM rimsky_node_runs
		 WHERE node_id = $1
		 ORDER BY sequence ASC`,
		[]any{worker.ID},
		func(scan func(...any) error) error {
			var rs shared.UUID
			if err := scan(&rs); err != nil {
				return err
			}
			runScopeIDs = append(runScopeIDs, rs)
			return nil
		})
	require.Len(t, runScopeIDs, 3,
		"loop-worker must have exactly 3 node-run rows (one per self-edge dispatch); got %d", len(runScopeIDs))
	require.Equal(t, runScopeIDs[0], runScopeIDs[1],
		"dispatch 1 and dispatch 2 must share one RunScope -- the self-edge loop stays inside one frame")
	require.Equal(t, runScopeIDs[1], runScopeIDs[2],
		"dispatch 2 and dispatch 3 must share one RunScope -- the self-edge loop stays inside one frame")

	var instanceFrameCount int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{iid}, &instanceFrameCount)
	require.Equal(t, 1, instanceFrameCount,
		"the entire 3-turn self-edge loop must stay inside one frame; got %d frames", instanceFrameCount)
}
