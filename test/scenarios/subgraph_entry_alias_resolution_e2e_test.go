// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSubgraphEntryAlias_ResolvesToCallingNodeAtRuntime(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller").Success(map[string]any{"token": "from-caller"}, true, "entered")
	h.Stub.WhenType("inner-mid").Success(map[string]any{}, true, "mid")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-entry-alias-resolution", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						scenario.WithAttributes(map[string]any{
							"type": "object",
							"properties": map[string]any{
								"token": map[string]any{"type": "string", "readOnly": true},
							},
						}),
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
						scenario.WithAttributes(map[string]any{
							"type": "object",
							"properties": map[string]any{
								"token": map[string]any{"type": "string", "readOnly": true},
							},
						}),
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-mid", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
								{Node: "inner-entry", Type: "attribute/token/changed", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						scenario.WithAttributes(map[string]any{
							"type": "object",
							"properties": map[string]any{
								"seen": map[string]any{
									"type":   "string",
									"source": "{{nodes.inner-entry.attribute.token}}",
								},
							},
						}),
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-mid", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						scenario.WithAttributes(map[string]any{
							"type": "object",
							"properties": map[string]any{
								"done": map[string]any{"type": "boolean", "readOnly": true},
							},
						}),
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-subgraph-entry-alias-resolution", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	midNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, midNode, "inner-mid node missing")
	exitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, exitNode, "inner-exit node missing")

	h.WaitForNodeState(midNode.ID, cascade.NodeStateFresh)

	var seen any
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		latest, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, midNode.ID, tx)
		if err != nil || latest == nil {
			return err
		}
		full, err := h.Persist.NodeAttributes().GetByRun(h.Ctx, latest.NodeRunID, tx)
		if err != nil || full == nil {
			return err
		}
		seen = full.Data["seen"]
		return nil
	}))
	require.Equal(t, "from-caller", seen,
		"an internal node's {{nodes.<entry-alias>.attribute.*}} substitution must resolve to the "+
			"CALLING node's per-invocation attribute bag (the entry is absorbed into the caller; the "+
			"entry alias has no runs of its own) — a missing value here means the entry-alias marker "+
			"has no runtime consumer")

	var boundToCaller int
	h.QueryRowSQL(`
		SELECT COUNT(*)
		  FROM rimsky_wait_set w
		  JOIN rimsky_node_runs sender   ON sender.id = w.sender_run_id
		  JOIN rimsky_node_runs receiver ON receiver.id = w.receiver_run_id
		 WHERE sender.node_id = $1
		   AND receiver.node_id = $2
	`, []any{callerNode.ID, midNode.ID}, &boundToCaller)
	require.GreaterOrEqual(t, boundToCaller, 1,
		"the entry-alias subscription edge must resolve to the calling node at the cascade walker: "+
			"inner-mid's run must hold a wait-set row whose sender is the CALLER's run (per-invocation "+
			"sender identity for the absorbed entry)")

	h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh)
	for {
		var callerState string
		h.QueryRowSQL(`
			SELECT COALESCE(state, 'fresh')
			  FROM rimsky_node_runs
			 WHERE node_id = $1
			 ORDER BY enqueued_at DESC
			 LIMIT 1
		`, []any{callerNode.ID}, &callerState)
		if callerState == string(cascade.NodeStateFresh) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}
