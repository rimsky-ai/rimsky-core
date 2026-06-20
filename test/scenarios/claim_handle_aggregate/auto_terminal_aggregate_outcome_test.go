// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claim_handle_aggregate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-auto-terminal-commit", map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	inheritor := h.FindNode(iid, "inheritor")
	require.NotNil(t, acquirer)
	require.NotNil(t, inheritor)

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 15*time.Second),
		"acquirer did not reach fresh")
	require.True(t, h.WaitForNodeState(inheritor.ID, cascade.NodeStateFresh, 15*time.Second),
		"inheritor did not reach fresh")

	deadline := time.Now().Add(2 * time.Second)
	var commitCount, abandonCount int
	for time.Now().Before(deadline) {
		commitCount, abandonCount = 0, 0
		for _, c := range sub.Calls() {
			switch c.Verb {
			case "commit":
				commitCount++
			case "abandon":
				abandonCount++
			}
		}
		if commitCount >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, 1, commitCount,
		"auto-terminal must fire exactly one commit for the held claim (aggregate-completed)")
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
	require.Greater(t, committedLhCount, 0,
		"at least one lock-holder row must be state=committed after auto-terminal commit")
}

func TestAutoTerminalAggregateFailedFiresGiveUp(t *testing.T) {
	t.Skip("scenario-level coverage delegated to " +
		"runtime/auto_terminal_test.go::TestCheckAndFireResolution_AnyFailedFiresGiveUp; " +
		"that unit test seeds claim-holder rows directly and exercises the " +
		"aggregate-failed → Abandon routing without needing executor-side error wiring")
}
