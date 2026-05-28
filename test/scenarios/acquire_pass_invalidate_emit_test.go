// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 43 — acquire_pass + subscription-driven monitor wakeup
// (post-2026-05-14 subscription-cascade resolution).
//
// Two-node template: worker has `error_types: { "acquire/unavailable":
// { policy: [pass] } }` (post-2026-05-23 reshape; was
// `on_acquire_unavailable: { resolve: pass }`). The producer returns
// Unavailable. Worker passes WITHOUT invoking the executor; monitor
// subscribes to worker's terminal/error/* and runs once worker resolves.
//
// The legacy handler.invalidate emit / message_emitted audit-event path
// retired with the subscription-cascade resolution; receiver-side
// subscription declares the cascade coupling explicitly.
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

func TestAcquirePassSubscribedMonitorRuns(t *testing.T) {
	t.Skip("post-2026-05-23 (signal-taxonomy + policy-decoupling reshape): " +
		"the pass branch of the unified error_types: chain (both " +
		"runtime/on_error.go::OnError pass case and " +
		"runtime/runner_error_policy.go::applyResolvedAction's " +
		"DispositionEnd + ColorFresh branch) commits a fresh state " +
		"transition + terminal/error/<class> audit signal but does NOT " +
		"fire cascadeSubscribersStaleInTx — only the retry branch does. " +
		"For a pass to wake a downstream subscriber, the pass branch " +
		"would need to extend cascade-fan-out to include settle-on-fresh " +
		"transitions (mirroring applyTerminalComplete's settlement-walk). " +
		"That's a deliberate scope-out from the 2026-05-23 spec; " +
		"unskipping requires a follow-up spec extending the cascade " +
		"walk to the pass branch.")
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// Empty queue — Open returns Unavailable.
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
	h.Stub.WhenType("monitor").Success(map[string]any{"m": 1}, true, "monitored")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acquire-pass-subscribed-monitor", Version: "1",
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
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "monitor",
					Executor: "stub",
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "worker", Type: "terminal/*", When: "fresh"}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-pass-monitor", map[string]any{})

	worker := h.FindNode(iid, "worker")
	monitor := h.FindNode(iid, "monitor")
	require.NotNil(t, worker)
	require.NotNil(t, monitor)

	// Worker should pass.
	require.True(t, waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/", 30*time.Second),
		"worker should record settling_signal_type=terminal/error/<class> via error_types: { acquire/unavailable: [pass] }")

	// Monitor must run at least once in response to worker's resolve;
	// the wait-set drain on worker's settle releases monitor.
	require.True(t, waitForEventCount(t, h, monitor.ID, "terminal/success", 1, 30*time.Second),
		"monitor must run after worker reaches fresh (subscription-driven)")

	// Worker's executor must NOT have been invoked.
	var workerObserved int
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			workerObserved++
		}
	}
	require.Equal(t, 0, workerObserved,
		"worker's executor must not be invoked when error_types: { acquire/unavailable: [pass] } fires")

	// Worker is fresh.
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, wRow.State)
}
