// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 29 — held_claim_acquirer_passes.
//
// A two-node template where A acquires a held claim from a stub queue
// producer and B inherits the claim. The producer returns Unavailable
// (queue is drained); A declares error_types: { "acquire/unavailable":
// { policy: [pass] } }.
//
// Asserts:
//   - A passes (settles fresh with settling_signal_type=terminal/error/<class>)
//     without invoking the executor.
//   - B is not woken — it subscribes to A's terminal/success, which does
//     not match A's terminal/error/<class> pass settlement. (A held-claim
//     inheritor inherits only on the acquirer's success; on acquisition
//     failure there is no claim to inherit and the held subgraph never
//     registers.)
//   - No rimsky_claim_handles rows exist for the never-acquired claim.
//   - No rimsky_claim_holders rows — the held subgraph never registered.
package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestHeldClaimAcquirerPasses(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// @deliberate: No InitialItems — Open returns Unavailable.
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "should-not-run")
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-claim-acquirer-passes", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "acquirer",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{{Action: "pass"}},
						},
					},
				},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "@queue", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-acq-passes", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	// @deliberate: Acquirer should settle fresh with settling_signal_type carrying
	// the canonical terminal/error/<class> envelope.
	require.True(t, waitForSettlingSignalTypePrefix(t, h, acq.ID, "terminal/error/", 30*time.Second),
		"acquirer should record settling_signal_type=terminal/error/<class> under error_types: { acquire/unavailable: [pass] }")

	// @constraint: Inheritor must NOT run — it subscribes to terminal/success, which
	// does not match the acquirer's terminal/error/<class> pass
	// settlement, so the cascade does not wake it. Give the system a beat.
	time.Sleep(2 * time.Second)

	var acqRow, inhRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().Get(h.Ctx, acq.ID, tx)
		if err != nil {
			return err
		}
		acqRow = ra
		ri, err := h.Persist.Nodes().Get(h.Ctx, inh.ID, tx)
		inhRow = ri
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, acqRow.State,
		"acquirer should be fresh after pass")
	require.Equal(t, cascade.NodeStateFresh, inhRow.State,
		"inheritor should remain fresh — pass should not cascade to it")

	// @constraint: Executor must not have been invoked for either node.
	require.Empty(t, h.Stub.Observed(),
		"executor must not be invoked when the acquirer passes on Unavailable")

	// @deliberate: No rimsky_claim_handles rows for this instance — the claim was
	// never acquired so no holder rows should exist.
	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Equal(t, 0, lhCount,
		"no claim_handle rows should exist when the producer returned Unavailable")

	// @deliberate: No rimsky_claim_holders rows for the held subgraph either.
	// Post-stage-5 the holder row keys on holder_run_id; join through
	// rimsky_node_runs → rimsky_nodes to scope by instance.
	var chCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_holders ch
		   JOIN rimsky_node_runs r ON r.id = ch.holder_run_id
		   JOIN rimsky_nodes n ON n.id = r.node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&chCount))
	require.Equal(t, 0, chCount,
		"no claim_holders rows should exist for an unacquired claim")
}
