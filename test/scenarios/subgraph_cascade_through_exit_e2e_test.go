// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSubgraphCascadeThroughExitE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")
	h.Stub.WhenType("downstream").Success(map[string]any{"ok": true}, true, "down")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-cascade-through-exit", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "downstream", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "caller", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
						},
						openAttrs,
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
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-subgraph-cascade-through-exit", map[string]any{})

	exitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, exitNode, "inner-exit node missing")
	downstreamNode := h.FindNode(iid, "downstream")
	require.NotNil(t, downstreamNode, "downstream node missing")

	h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh)

	h.WaitForNodeState(downstreamNode.ID, cascade.NodeStateFresh)

	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
	var inMain int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_node_runs
		 WHERE node_id = $1 AND run_scope_id = $2
	`, []any{downstreamNode.ID, mainScopeID}, &inMain)
	require.GreaterOrEqual(t, inMain, 1,
		"downstream's run must live in the main RunScope, not the sub-graph scope")
}

func TestSubgraphEntryAliasSubscriptionAndNilTemplateExitE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-middle").Success(map[string]any{"mid": true}, true, "mid")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":  map[string]any{"type": "boolean", "readOnly": true},
			"mid": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-entry-alias-nil-exit", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(node.TemplateNodeDef{Type: "caller", Delegate: "worker"}, openAttrs),
				},
			},
			{
				Name:  "worker",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(node.TemplateNodeDef{Type: "inner-entry", Executor: "stub"}, openAttrs),
					scenario.MakeNode(node.TemplateNodeDef{Type: "inner-middle", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
					}, openAttrs),
					scenario.MakeNode(node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-middle", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
					}),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-subgraph-entry-alias-nil-exit", map[string]any{})

	caller := h.FindNode(iid, "caller")
	middle := h.FindNode(iid, "inner-middle")
	exit := h.FindNode(iid, "inner-exit")
	require.NotNil(t, caller)
	require.NotNil(t, middle, "an internal node subscribing to the absorbed entry alias must still be instantiated")
	require.NotNil(t, exit)

	h.WaitForNodeState(middle.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(exit.ID, cascade.NodeStateFresh)
	waitForLatestRunState(t, h, caller.ID, cascade.NodeStateFresh)

	middleIdx, exitIdx := -1, -1
	for i, obs := range h.Stub.Observed() {
		switch obs.NodeType {
		case "inner-middle":
			if middleIdx == -1 {
				middleIdx = i
			}
		case "inner-exit":
			if exitIdx == -1 {
				exitIdx = i
			}
		}
	}
	require.GreaterOrEqual(t, middleIdx, 0, "inner-middle must actually dispatch")
	require.GreaterOrEqual(t, exitIdx, 0, "inner-exit must actually dispatch")
	require.Less(t, middleIdx, exitIdx,
		"the exit subscribes to inner-middle and must dispatch after it settles, proving the internal cascade order survives entry absorption")

	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
	for {
		var childScopes, closedChildScopes int
		h.QueryRowSQL(`
			SELECT COUNT(*), COUNT(closed_at)
			  FROM rimsky_run_scopes
			 WHERE instance_id = $1 AND parent_run_scope_id = $2 AND graph_name = 'worker'`,
			[]any{iid, mainScopeID}, &childScopes, &closedChildScopes)
		require.Equal(t, 1, childScopes, "the delegate call must open exactly one child RunScope under main")
		if closedChildScopes == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForLatestRunState(t *testing.T, h *scenario.Harness, nodeID any, want cascade.NodeState) {
	t.Helper()
	for {
		var state string
		h.QueryRowSQL(`
			SELECT COALESCE(state::text, '')
			  FROM rimsky_node_runs
			 WHERE node_id = $1
			 ORDER BY enqueued_at DESC
			 LIMIT 1`,
			[]any{nodeID}, &state)
		if state == string(want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
