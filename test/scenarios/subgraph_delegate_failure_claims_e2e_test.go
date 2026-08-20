// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSubgraphDelegateFailure_ResolvesCallerClaims(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"delegate-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "entered")
	h.Stub.WhenType("inner-mid").Error("subgraph_doom", map[string]any{"why": "internal failure"})
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-delegate-failure-claims", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
						scenario.WithClaimProducers(scenario.AliasedClaimRef("delegate-store", "data", "rw", "data")),
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
	})
	iid := h.CreateInstance(tid, "ck-subgraph-delegate-failure-claims", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	midNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, midNode, "inner-mid node missing")

	awaited.Until(t, "the caller node to hold a claim_handle row", func() bool {
		var claimRows int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_claim_handles
			 WHERE holder_node_id = $1
		`, []any{callerNode.ID}, &claimRows)
		return claimRows >= 1
	})

	h.WaitForNodeState(midNode.ID, cascade.NodeStateFailed)

	awaited.Until(t, "the caller node's latest run to fail", func() bool {
		var callerState string
		h.QueryRowSQL(`
			SELECT COALESCE(state, 'fresh')
			  FROM rimsky_node_runs
			 WHERE node_id = $1
			 ORDER BY enqueued_at DESC
			 LIMIT 1
		`, []any{callerNode.ID}, &callerState)
		return callerState == string(cascade.NodeStateFailed)
	})

	awaited.Until(t, "the caller node's claim_handles to leave the active state", func() bool {
		var activeClaims int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_claim_handles
			 WHERE holder_node_id = $1
			   AND state = 'active'
		`, []any{callerNode.ID}, &activeClaims)
		return activeClaims == 0
	})
}
