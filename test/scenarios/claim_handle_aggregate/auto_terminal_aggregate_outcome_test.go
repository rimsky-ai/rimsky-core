// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claim_handle_aggregate

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAutoTerminalAggregateCommitEndToEnd(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "inherited")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "auto-terminal-commit", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("content", "/region-held", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-auto-terminal-commit", map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	inheritor := h.FindNode(iid, "inheritor")
	require.NotNil(t, acquirer)
	require.NotNil(t, inheritor)

	h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(inheritor.ID, cascade.NodeStateFresh)

	commitCount, abandonCount := waitForAtLeastOneProducerVerbDelivery(t, sub, "commit")
	require.GreaterOrEqual(t, commitCount, 1,
		"auto-terminal must fire at least one commit for the held claim (aggregate-completed); "+
			"the terminal-verb outbox delivers at-least-once, so >1 is a legitimate retry, not a bug")
	require.Equal(t, 0, abandonCount,
		"aggregate-completed must NOT route to Abandon")

	var activeLhCount, committedLhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'active'`, iid,
	).Scan(&activeLhCount))
	require.Equal(t, 0, activeLhCount,
		"no active lock-holder rows must remain after auto-terminal commit")
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'committed'`, iid,
	).Scan(&committedLhCount))
	require.Equal(t, 1, committedLhCount,
		"exactly one claim_handle row (the single shared 'held' claim acquired by the acquirer and "+
			"inherited by the inheritor) must be state=committed after auto-terminal commit — an "+
			"over-commit or duplicate-row regression would show up here")
}

func waitForAtLeastOneProducerVerbDelivery(t *testing.T, sub *stubstore.Store, wantVerb string) (commitCount, abandonCount int) {
	t.Helper()
	awaited.Until(t, "the stub producer to receive a "+wantVerb+" verb", func() bool {
		commitCount, abandonCount = 0, 0
		seenWant := false
		for _, c := range sub.Calls() {
			switch c.Verb {
			case "commit":
				commitCount++
			case "abandon":
				abandonCount++
			}
			if c.Verb == wantVerb {
				seenWant = true
			}
		}
		return seenWant
	})
	return commitCount, abandonCount
}

func TestAutoTerminalAggregateFailedFiresGiveUp(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("inheritor").Error("forced", map[string]any{"why": "rolling-back"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "auto-terminal-failed-giveup", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("content", "/region-held-failed", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/forced": {Action: "give_up"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-auto-terminal-failed-giveup", map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	inheritor := h.FindNode(iid, "inheritor")
	require.NotNil(t, acquirer)
	require.NotNil(t, inheritor)

	h.WaitForNodeState(inheritor.ID, cascade.NodeStateFailed)
	h.WaitForNodeState(acquirer.ID, cascade.NodeStateFailed)

	commitCount, abandonCount := waitForAtLeastOneProducerVerbDelivery(t, sub, "abandon")
	require.GreaterOrEqual(t, abandonCount, 1,
		"aggregate-failed must fire at least one Abandon call to the producer (auto-terminal give_up routing); "+
			"the terminal-verb outbox delivers at-least-once, so >1 is a legitimate retry, not a bug")
	require.Equal(t, 0, commitCount,
		"aggregate-failed must NOT route to Commit")

	var abandonedLhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'abandoned'`, iid,
	).Scan(&abandonedLhCount))
	require.Equal(t, 1, abandonedLhCount,
		"the shared 'held' claim must land in state=abandoned, not committed, after the inheritor's give_up")
}
