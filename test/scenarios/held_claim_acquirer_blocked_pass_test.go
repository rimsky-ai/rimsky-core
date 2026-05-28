// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Held-claim acquirer + executor returns an error with
// error_class=executor_blocked + error_types: { executor_blocked: { policy: [pass] } }
// (post-2026-05-23 reshape; was on_executor_errored: {resolve: pass}).
// Validates that auto-terminal fires immediately on the acquirer's
// pass-resolution: the held subgraph aborts (acquirer failed to produce
// work) and rimsky_claim_handles is released without waiting for
// inheritors to reach a terminal they would never reach.
//
// Regression coverage for the leak where the pass-resolution path on
// a held claim (now `applyErrorPolicy::applyResolvedAction`'s
// `DispositionEnd + ColorFresh` branch in `runner_error_policy.go`)
// only marked the acquirer's claim_holders row, leaving inheritors'
// rows in 'active' indefinitely and stranding the rimsky_claim_handles
// row + remaining rimsky_claim_holders rows.
package scenarios

import (
	"encoding/json"
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

func TestHeldClaimAcquirerBlockedPass(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
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
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	// Acquirer's executor returns an executor-blocked error. Inheritor
	// would succeed if it ran — it must not. Under the 2026-05-23
	// signal-taxonomy reshape the pass-resolution path on the acquirer
	// settles fresh with settling_signal_type=terminal/error/<class>
	// but does NOT fire cascadeSubscribersStaleInTx (only the retry
	// branch does); on top of that the held-subgraph abort means
	// inheritors are explicitly failed in the claim_holders table.
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
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/executor_blocked": {
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
				},
				scenario.WithInherits(scenario.Inherit("held")),
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*"}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-acq-blocked-pass", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	// Acquirer should settle fresh under the pass branch.
	require.True(t, waitForSettlingSignalTypePrefix(t, h, acq.ID, "terminal/error/", 30*time.Second),
		"acquirer should record settling_signal_type=terminal/error/<class> under error_types: { executor_blocked: { policy: [pass] } }")

	// Auto-terminal must fire promptly because the acquirer-failure
	// path now fails all inheritor claim_holders rows (the fix for the
	// held-claim leak). Post-Stage-3 of the claim-handle state-column
	// refactor: auto-terminal flips the row's state (Promote-not-
	// delete) rather than deleting it. Validate that every claim-handle
	// for this instance is in a terminal state (committed or abandoned)
	// without waiting for inheritor terminals.
	deadline := time.Now().Add(30 * time.Second)
	var activeCount int
	for time.Now().Before(deadline) {
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_claim_handles lh
			   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			  WHERE n.instance_id = $1 AND lh.state = 'active'`, uuid.UUID(iid),
		).Scan(&activeCount))
		if activeCount == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, 0, activeCount,
		"every rimsky_claim_handles row for this instance must reach a terminal state — auto-terminal must fire when the held-claim acquirer takes resolve=pass; non-zero indicates the inheritor-rows-active leak has regressed")

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
