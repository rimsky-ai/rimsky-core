// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Held-claim acquirer + executor returns an error with
// error_class=executor_blocked + on_executor_errored: {resolve: pass}.
// Validates that auto-terminal fires immediately on the acquirer's
// pass-resolution: the held subgraph aborts (acquirer failed to produce
// work) and rimsky_claim_handles is released without waiting for
// inheritors to reach a terminal they would never reach.
//
// Regression coverage for the leak where applyTerminalPass on a held
// claim only marked the acquirer's claim_holders row, leaving
// inheritors' rows in 'active' indefinitely and stranding the
// rimsky_claim_handles row + remaining rimsky_claim_holders rows.
package scenarios

import (
	"encoding/json"
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

func TestHeldClaimAcquirerBlockedPass(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// One item so Open returns Acquired and we exercise the
				// post-acquisition Blocked path (vs. the
				// on_acquire_unavailable path in
				// held_claim_acquirer_passes_test.go).
				InitialItems: []json.RawMessage{
					json.RawMessage(`{"i":1}`),
				},
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
	// Acquirer's executor returns an executor-blocked error. Inheritor
	// would succeed if it ran — it must not (the cascade-firing gate is
	// fresh_changed; passed does not propagate, and on top of that the
	// held-subgraph abort means inheritors are explicitly failed in the
	// claim_holders table).
	h.Stub.WhenType("acquirer").Error("executor_blocked", map[string]any{
		"reason": "blocked_class",
		"why":    "stub-blocked",
	})
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-claim-acquirer-blocked-pass", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "acquirer",
					Executor: "stub",
					OnExecutorErrored: &node.OnExecutorTerminalHandler{
						Resolve: node.ResolvePass,
					},
				},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "@queue", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
				},
				scenario.WithInherits(scenario.Inherit("held")),
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", On: "state"}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-acq-blocked-pass", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	// Acquirer should land in fresh+passed.
	require.True(t, waitForLastOutcome(t, h, acq.ID, cascade.LastOutcomePassed, 30*time.Second),
		"acquirer should record last_outcome=passed under on_executor_errored: pass")

	// Auto-terminal must fire promptly because the acquirer-failure
	// path now fails all inheritor claim_holders rows (the fix for the
	// held-claim leak). Validate that rimsky_claim_handles for this
	// instance reaches 0 without waiting for inheritor terminals.
	deadline := time.Now().Add(30 * time.Second)
	var lhCount int
	for time.Now().Before(deadline) {
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_claim_handles lh
			   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			  WHERE n.instance_id = $1`, uuid.UUID(iid),
		).Scan(&lhCount))
		if lhCount == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, 0, lhCount,
		"rimsky_claim_handles rows must reach 0 — auto-terminal must fire when the held-claim acquirer takes resolve=pass; non-zero indicates the inheritor-rows-active leak has regressed")

	// rimsky_claim_holders rows should also be drained (auto-terminal
	// deletes the parent claim_handle which CASCADEs claim_holders).
	var chCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_holders ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&chCount))
	require.Equal(t, 0, chCount,
		"rimsky_claim_holders rows must be drained alongside the parent claim_handle")

	// Inheritor must remain fresh — passed does not cascade.
	var inhRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, inh.ID, tx)
		inhRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, inhRow.State,
		"inheritor should remain fresh — pass on the acquirer must not propagate")
}
