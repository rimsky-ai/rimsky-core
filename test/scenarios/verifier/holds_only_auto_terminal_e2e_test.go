// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @deliberate: #2 — holds:-only co-holdership engages the held auto-terminal path.
//
// A two-node template where an acquirer node opens a claim and a
// downstream co-holder declares `holds: {held: {from: acquirer}}`.
// `holds:` is the sole co-holdership directive (the legacy `inherits:`
// directive was removed). Both nodes drive to terminal success through
// the loopback stub fixture. The documented contract
// (@blessed-invariant 13, concept:claim-co-holdership) is that a
// `holds:`-declared claim is HELD: the acquirer's own claim-holders row
// is seeded at acquire, the co-holder's row is seeded at its acquire,
// and at holding-subgraph completion auto-terminal fires exactly one
// aggregate Commit over the co-holder set, then promotes the claim
// handle to state='committed'.
//
// The held-subgraph DETECTION layer
// (`lib/graph/node/inheritance.go::HoldingSubgraphsForTemplate` /
// `IsHeld`) builds subgraph members from `Holds`, so:
//
//   - `IsHeld()` is true for a holds-only claim, so the acquirer's own
//     `rimsky_claim_holders` row IS seeded at acquire
//     (`insertHeldClaimHoldersAtAcquire` gates on `IsHeld()`).
//   - At the acquirer's release `isAliasHeld()` is true, so the
//     acquirer takes the held branch and routes through
//     `CheckAndFireResolution` over the co-holder set rather than
//     committing immediately.
//   - The co-holder's `rimsky_claim_holders` row is aggregated by the
//     held auto-terminal path and never stranded `state='active'`.
//
// The orphaned-`active`-claim_holders assertion is the load-bearing
// distinguisher: a single `committed` claim-handle row alone does NOT
// prove the held path fired (a non-held path would also promote the
// row to `committed`). Only the held auto-terminal path resolves every
// co-holder row — so a remaining `active` co-holder row would be the
// fingerprint of missing held-subgraph detection.
package verifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestHoldsOnlyAutoTerminal deploys an acquirer + a `holds:`-only
// co-holder, drives both to terminal success, and asserts the held
// auto-terminal path fired: the acquirer's claim handle is
// `committed`, no co-holder claim_holders row is left orphaned, and
// exactly one store Commit fired over the co-holder set.
func TestHoldsOnlyAutoTerminal(t *testing.T) {
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
	h.Stub.WhenType("coholder").Success(map[string]any{}, true, "co-held")

	// @deliberate: holds:-only template. The acquirer declares the `held` claim
	// alias; the co-holder co-holds it via `holds: {held: {from:
	// acquirer}}` and subscribes to the acquirer's terminal so it
	// runs after acquisition. `holds:` is the sole co-holdership
	// directive — this exercises the holds-based detection path.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "holds-only-auto-terminal", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("content", "/region-held", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "coholder",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-holds-only-auto-terminal", map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	coholder := h.FindNode(iid, "coholder")
	require.NotNil(t, acquirer)
	require.NotNil(t, coholder)

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 15*time.Second),
		"acquirer did not reach fresh")
	require.True(t, h.WaitForNodeState(coholder.ID, cascade.NodeStateFresh, 15*time.Second),
		"co-holder did not reach fresh")

	// @constraint: Auto-terminal must fire exactly one Commit over the co-holder set
	// (aggregate-completed → Commit per @blessed-invariant 13). Abandon
	// must not fire. Poll briefly for the held resolution to settle.
	deadline := time.Now().Add(5 * time.Second)
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
		"a holds:-only claim must drive exactly one aggregate Commit over the co-holder set")
	require.Equal(t, 0, abandonCount,
		"aggregate-completed (all-success) must NOT route to Abandon")

	// @deliberate: LOAD-BEARING DISTINGUISHER: every co-holder claim_holders row must
	// be resolved by the held auto-terminal path. Today the acquirer
	// commits via the non-held branch and never aggregates the
	// co-holder's row, leaving it stranded `state='active'`. The
	// holders row keys on holder_run_id; join through rimsky_node_runs →
	// rimsky_nodes to scope by instance.
	var activeHolderCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_claim_holders ch
		   JOIN rimsky_node_runs r ON r.id = ch.holder_run_id
		   JOIN rimsky_nodes n ON n.id = r.node_id
		  WHERE n.instance_id = $1 AND ch.state = 'active'`,
		[]any{iid}, &activeHolderCount,
	)
	require.Equal(t, 0, activeHolderCount,
		"no rimsky_claim_holders row may remain 'active' after the held auto-terminal fires — "+
			"a stranded co-holder row means the holds:-only claim was never recognized as held "+
			"and the documented aggregate Commit never resolved the co-holder set")

	// @deliberate: The acquirer's claim handle must reach state='committed' via the
	// held auto-terminal path (Promote-not-delete). Required assertion
	// per the task; on its own it does not distinguish the held path
	// from the broken non-held path (both promote to committed), which
	// is why the orphaned-active-holders assertion above is the
	// load-bearing one.
	var committedHandleCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'committed'`,
		[]any{iid}, &committedHandleCount,
	)
	require.Greater(t, committedHandleCount, 0,
		"the acquirer's claim handle must reach state='committed' after the held auto-terminal Commit")

	// @deliberate: And no claim handle may be left 'active'.
	var activeHandleCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'active'`,
		[]any{iid}, &activeHandleCount,
	)
	require.Equal(t, 0, activeHandleCount,
		"no claim handle may remain 'active' after both nodes settle")
}
