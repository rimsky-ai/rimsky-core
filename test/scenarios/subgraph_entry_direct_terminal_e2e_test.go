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

func subgraphEntryDirectTerminalTemplate() node.TemplateSpec {
	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})
	return node.TemplateSpec{
		Name: "subgraph-entry-direct-terminal", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					{Type: "caller", Delegate: "worker"},
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
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-mid", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
						},
						openAttrs,
					),
				},
			},
		},
	}
}

// @concept: delegation
func TestSubgraphEntryDirectError_TerminatesCallerWithNoInternalCascade(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller").Error("entry_doom", map[string]any{"why": "entry failed before ever succeeding"})

	tid := h.DeployTemplate(subgraphEntryDirectTerminalTemplate())
	iid := h.CreateInstance(tid, "ck-subgraph-entry-direct-error", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	innerMidNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, innerMidNode, "inner-mid node missing")
	innerExitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, innerExitNode, "inner-exit node missing")

	h.WaitForNodeState(callerNode.ID, cascade.NodeStateFailed)

	for _, internal := range []struct {
		typ    string
		nodeID interface{}
	}{
		{"inner-mid", innerMidNode.ID},
		{"inner-exit", innerExitNode.ID},
	} {
		var dispatchCount int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1
		`, []any{internal.nodeID}, &dispatchCount)
		require.Equal(t, 0, dispatchCount,
			"%s must never be dispatched: the absorbed entry failed directly (not via the success "+
				"branch's internal cascade), so applyTerminalError must terminate the caller with no "+
				"internal subgraph nodes ever running", internal.typ)
	}
}

// @concept: delegation
func TestSubgraphEntryDirectPark_TerminatesCallerWithNoInternalCascade(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	resumeAt := time.Now().Add(15 * time.Second)
	h.Stub.WhenType("caller").Park(resumeAt)

	tid := h.DeployTemplate(subgraphEntryDirectTerminalTemplate())
	iid := h.CreateInstance(tid, "ck-subgraph-entry-direct-park", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	innerMidNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, innerMidNode, "inner-mid node missing")
	innerExitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, innerExitNode, "inner-exit node missing")

	h.WaitForNodeState(callerNode.ID, cascade.NodeStateParked)

	for _, internal := range []struct {
		typ    string
		nodeID interface{}
	}{
		{"inner-mid", innerMidNode.ID},
		{"inner-exit", innerExitNode.ID},
	} {
		var dispatchCount int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1
		`, []any{internal.nodeID}, &dispatchCount)
		require.Equal(t, 0, dispatchCount,
			"%s must never be dispatched: the absorbed entry parked directly (not via the success "+
				"branch's internal cascade), so applyTerminalPark must terminate the caller with no "+
				"internal subgraph nodes ever running", internal.typ)
	}
}
