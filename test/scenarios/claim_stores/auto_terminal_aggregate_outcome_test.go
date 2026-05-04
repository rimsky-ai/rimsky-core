// Auto-terminal aggregate-outcome scenario coverage — invariant 13
// (held-claim resolution at holding-subgraph completion).
//
// Test 1 (`TestAutoTerminalAggregateCommitEndToEnd`) drives a two-node
// template (acquirer + held inheritor) end-to-end through the loopback
// stub fixture and asserts that:
//   - exactly one store `commit` verb fires for the held claim
//     (aggregate-completed → Commit per spec §4.10 invariant 13).
//   - zero `rimsky_lock_holders` rows remain for the instance after
//     both nodes reach `fresh`.
//   - zero `rimsky_claim_holders` rows remain (cascade FK cleans them
//     when the lock-holder row is deleted).
//
// Test 2 (`TestAutoTerminalAggregateFailedFiresGiveUp`) is delegated to
// the unit-level coverage in
// `core/supervisor/auto_terminal_test.go::TestCheckAndFireResolution_AnyFailedFiresGiveUp`,
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

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

// TestAutoTerminalAggregateCommitEndToEnd deploys an acquirer + held
// inheritor, lets both reach `fresh`, and asserts the auto-terminal
// mechanism fired exactly one `commit` against the store.
func TestAutoTerminalAggregateCommitEndToEnd(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Complete(map[string]any{}, true, "acquired")
	h.Stub.WhenType("inheritor").Complete(map[string]any{}, true, "inherited")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "auto-terminal-commit", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("content", "/region-held", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "inheritor", Executor: "stub", Dependencies: []string{"acquirer"}},
				scenario.WithInherits(scenario.Inherit("held")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-auto-terminal-commit", map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	inheritor := h.FindNode(iid, "inheritor")
	require.NotNil(t, acquirer)
	require.NotNil(t, inheritor)

	require.True(t, h.WaitForNodeState(acquirer.ID, shared.NodeStateFresh, 15*time.Second),
		"acquirer did not reach fresh")
	require.True(t, h.WaitForNodeState(inheritor.ID, shared.NodeStateFresh, 15*time.Second),
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

	// Lock-holder + claim-holder rows are gone.
	var lhCount, chCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_lock_holders lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1`, iid,
	).Scan(&lhCount))
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_holders ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1`, iid,
	).Scan(&chCount))
	require.Equal(t, 0, lhCount, "lock-holder rows must be cleaned up after auto-terminal commit")
	require.Equal(t, 0, chCount, "claim-holder rows must be cascade-deleted with the lock-holder")
}

// TestAutoTerminalAggregateFailedFiresGiveUp delegates to the unit-level
// coverage. Wiring an executor-side give_up class through the template
// DSL plus the loopback stub fixture in a deterministic way is a much
// larger lift than the property warrants given the unit test directly
// pins the routing decision.
func TestAutoTerminalAggregateFailedFiresGiveUp(t *testing.T) {
	t.Skip("scenario-level coverage delegated to " +
		"core/supervisor/auto_terminal_test.go::TestCheckAndFireResolution_AnyFailedFiresGiveUp; " +
		"that unit test seeds claim-holder rows directly and exercises the " +
		"aggregate-failed → Abandon routing without needing executor-side error wiring")
}
