// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 29 — held_claim_acquirer_passes.
//
// A two-node template where A acquires a held claim from a stub queue
// producer and B inherits the claim. The producer returns Unavailable
// (queue is drained); A has on_acquire_unavailable: { resolve: pass }.
//
// Asserts:
//   - A passes (fresh+passed) without invoking the executor.
//   - B is not woken — the cascade-firing gate is last_outcome ==
//     fresh_changed; passed does not propagate.
//   - No rimsky_claim_handles rows exist for the never-acquired claim.
//   - No rimsky_claim_holders rows — the held subgraph never registered.
package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/control/config"
	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
	"github.com/fallguy/rimsky/stores/common/action"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

func TestHeldClaimAcquirerPasses(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// No InitialItems — Open returns Unavailable.
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "should-not-run")
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-claim-acquirer-passes", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "acquirer",
					Executor: "stub",
					OnAcquireUnavailable: &node.OnAcquireUnavailableHandler{
						Resolve: node.ResolvePass,
					},
				},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "@queue", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:         "inheritor",
					Executor:     "stub",
					Dependencies: []string{"acquirer"},
				},
				scenario.WithInherits(scenario.Inherit("held")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-acq-passes", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	// Acquirer should land in fresh+passed.
	require.True(t, waitForLastOutcome(t, h, acq.ID, cascade.LastOutcomePassed, 30*time.Second),
		"acquirer should record last_outcome=passed under on_acquire_unavailable: pass")

	// Inheritor must NOT run — pass does not cascade (last_outcome=passed
	// doesn't satisfy the fresh_changed gate). Give the system a beat.
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

	// Executor must not have been invoked for either node.
	require.Empty(t, h.Stub.Observed(),
		"executor must not be invoked when the acquirer passes on Unavailable")

	// No rimsky_claim_handles rows for this instance — the claim was
	// never acquired so no holder rows should exist.
	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Equal(t, 0, lhCount,
		"no claim_handle rows should exist when the producer returned Unavailable")

	// No rimsky_claim_holders rows for the held subgraph either.
	var chCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_holders ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&chCount))
	require.Equal(t, 0, chCount,
		"no claim_holders rows should exist for an unacquired claim")
}
