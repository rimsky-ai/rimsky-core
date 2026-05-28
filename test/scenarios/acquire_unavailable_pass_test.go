// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 26 — error_types: { "acquire/unavailable": { policy: [pass] } }.
// A node whose claim-producer returns Unavailable on its required claim
// transitions stale → fresh and records settling_signal_type =
// terminal/error/acquire/unavailable (pass-color per Resolution.Color);
// the executor is not invoked; no cascade-on-commit fires.
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

// TestAcquireUnavailablePass starts a stub claim-producer with an empty
// pick-policy queue (selector "@queue"), so the producer's Open returns
// Unavailable. The node declares error_types: { "acquire/unavailable":
// { policy: [pass] } } — the supervisor must transition the node to
// fresh (with settling_signal_type = terminal/error/acquire/unavailable)
// without invoking the executor.
func TestAcquireUnavailablePass(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// No InitialItems — the queue is drained from the start.
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
	// Script the executor — but we expect it to never be called.
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-pass", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{{Action: "pass"}},
						},
					},
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Node should record settling_signal_type=terminal/error/acquire/unavailable.
	require.True(t, waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/", 30*time.Second),
		"worker should record settling_signal_type=terminal/error/acquire/unavailable under error_types: { acquire/unavailable: [pass] }")

	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, wRow.State,
		"worker should be fresh after resolve=pass")

	// The stub producer must have seen at least one Open (the one that
	// returned Unavailable). The executor must not have been invoked.
	var sawOpen bool
	for _, c := range sub.Calls() {
		if c.Verb == "open" {
			sawOpen = true
			break
		}
	}
	require.True(t, sawOpen, "stub producer should have received at least one Open")

	// Stub executor's observed-request log should be empty.
	require.Empty(t, h.Stub.Observed(),
		"executor must not be invoked when error_types: { acquire/unavailable: [pass] } fires")
}
