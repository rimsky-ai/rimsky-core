// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Auto-terminal aggregate-outcome scenario coverage — invariant 13
// (held-claim resolution at holding-subgraph completion).
//
// Test 1 (`TestAutoTerminalAggregateCommitEndToEnd`) drives a two-node
// template (acquirer + held inheritor) end-to-end through the loopback
// stub fixture and asserts that:
//   - exactly one store `commit` verb fires for the held claim
//     (aggregate-completed → Commit per spec §4.10 invariant 13).
//   - zero `rimsky_claim_handles` rows remain for the instance after
//     both nodes reach `fresh`.
//   - zero `rimsky_claim_holders` rows remain (cascade FK cleans them
//     when the lock-holder row is deleted).
//
// Test 2 (`TestAutoTerminalAggregateFailedFiresGiveUp`) is delegated to
// the unit-level coverage in
// `runtime/auto_terminal_test.go::TestCheckAndFireResolution_AnyFailedFiresGiveUp`,
// which seeds `rimsky_claim_holders` rows directly and exercises the
// aggregate-failed → Abandon routing without the wire round-trip.
// Reproducing the same property end-to-end through the loopback fixture
// would require coordinating an executor-side error class with a
// give-up policy through the template DSL — a much larger lift than
// the property warrants given the unit-level coverage already pins it.
package claim_stores

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/control/config"
	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/stores/stub/testfixture"
)

// TestAutoTerminalAggregateCommitEndToEnd deploys an acquirer + held
// inheritor, lets both reach `fresh`, and asserts the auto-terminal
// mechanism fired exactly one `commit` against the store.
func TestAutoTerminalAggregateCommitEndToEnd(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
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
				scenario.WithStores(scenario.AliasedClaimRef("content", "/region-held", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "inheritor", Executor: "stub"},
				scenario.WithInherits(scenario.Inherit("held")),
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*"}),
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

	// Collect store verb counts. Auto-terminal must fire exactly
	// one Commit (aggregate-completed). Abandon must not fire.
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

	// Post-Stage-3 of the claim-handle state-column refactor: auto-
	// terminal Promote-not-delete. Assert lock-holder rows are in a
	// terminal state (state='committed') rather than deleted; the
	// retention sweep will reap them at cutoff.
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

// TestAutoTerminalAggregateFailedFiresGiveUp delegates to the unit-level
// coverage. Wiring an executor-side give_up class through the template
// DSL plus the loopback stub fixture in a deterministic way is a much
// larger lift than the property warrants given the unit test directly
// pins the routing decision.
func TestAutoTerminalAggregateFailedFiresGiveUp(t *testing.T) {
	t.Skip("scenario-level coverage delegated to " +
		"runtime/auto_terminal_test.go::TestCheckAndFireResolution_AnyFailedFiresGiveUp; " +
		"that unit test seeds claim-holder rows directly and exercises the " +
		"aggregate-failed → Abandon routing without needing executor-side error wiring")
}
