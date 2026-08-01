// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func absorbedEntryDelegationTemplate(name string) node.TemplateSpec {
	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})
	return node.TemplateSpec{
		Name: name, Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
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
						node.TemplateNodeDef{Type: "inner-mid", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-mid", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
				},
			},
		},
	}
}

func requireNoSubgraphSpunUp(t *testing.T, h *scenario.Harness, iid shared.UUID) {
	t.Helper()
	var subScopes int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND graph_name = 'worker'
	`, []any{iid}, &subScopes)
	require.Equal(t, 0, subScopes,
		"no sub-graph RunScope (graph_name='worker') may be created when the absorbed-entry "+
			"caller itself fails/parks before ever reaching applyTerminalCompleteSubgraphCaller "+
			"(that constructor only runs on the success branch of applyTerminalComplete)")

	var internalRuns int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_node_runs r
		  JOIN rimsky_nodes n ON n.id = r.node_id
		 WHERE n.instance_id = $1 AND n.node_type IN ('inner-mid', 'inner-exit')
	`, []any{iid}, &internalRuns)
	require.Equal(t, 0, internalRuns,
		"no sub-graph internal node (inner-mid, inner-exit) may ever be dispatched when the "+
			"absorbed-entry caller fails/parks; SubgraphParentSuccessCascade is only invoked "+
			"from the success terminal, never from applyTerminalError/applyTerminalPark")
}

func TestTemplateSubGraphDelegation_AbsorbedEntryError_ParentTerminatesWithNoInternalCascade(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller").Error("entry_doom", map[string]any{"why": "entry-level failure"})
	h.Stub.WhenType("inner-mid").Success(map[string]any{"ok": true}, true, "mid")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	tid := h.DeployTemplate(absorbedEntryDelegationTemplate("story-subgraph-delegation-entry-error"))
	iid := h.CreateInstance(tid, "ck-story-subgraph-delegation-entry-error", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")

	h.WaitForNodeState(callerNode.ID, cascade.NodeStateFailed)

	requireNoSubgraphSpunUp(t, h, iid)
}

func TestTemplateSubGraphDelegation_AbsorbedEntryPark_ParentParksWithNoInternalCascade(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller").
		Park(time.Now().Add(time.Hour))
	h.Stub.WhenType("inner-mid").Success(map[string]any{"ok": true}, true, "mid")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	tid := h.DeployTemplate(absorbedEntryDelegationTemplate("story-subgraph-delegation-entry-park"))
	iid := h.CreateInstance(tid, "ck-story-subgraph-delegation-entry-park", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")

	h.WaitForNodeState(callerNode.ID, cascade.NodeStateParked)

	requireNoSubgraphSpunUp(t, h, iid)
}
