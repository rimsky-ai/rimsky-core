// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 28 — error_types: { "acquire/unavailable": { policy: [give_up] } }.
// The stub producer returns Unavailable; the error_types policy fires
// against the synthetic class and drives the node into the failed
// state.
package scenarios

import (
	"testing"
	"time"

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

// TestAcquireUnavailableErrorRouting drives the resolve=error path:
// the operator declares an error_class that maps to give_up; the
// supervisor routes through OnError; the node lands in failed.
func TestAcquireUnavailableErrorRouting(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
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
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-error-routing", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					// Post-2026-05-23: on_acquire_unavailable retires;
					// acquisition failure routes via synthetic class
					// "acquire/unavailable" in error_types:. give_up
					// drives the node into failed (matching the prior
					// behavior of resolve=error pointing at give_up).
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-err", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// give_up should drive the node to failed.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 30*time.Second),
		"worker should land in failed via error_types: { acquire/unavailable: [give_up] }")

	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.NotNil(t, wRow.SettlingSignalType)
	require.Contains(t, *wRow.SettlingSignalType, "terminal/error/",
		"give_up should record settling_signal_type=terminal/error/<class>")

	// Executor must not have been invoked.
	require.Empty(t, h.Stub.Observed(),
		"executor must not be invoked when error_types: { acquire/unavailable: [give_up] } fires")
}
