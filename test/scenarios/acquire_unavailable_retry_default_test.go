// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 27 — opt-in retry under acquire/unavailable. Post-2026-05-23
// the default behavior changed from implicit silent-retry to fail-fast
// (give_up("unknown_error_class")). Operators that want retry now
// declare it explicitly via `error_types: { "acquire/unavailable":
// { policy: [retry] } }`. Verified by seeding an item into the empty
// queue mid-run and confirming the node ultimately reaches fresh under
// the explicitly-declared retry chain.
package scenarios

import (
	"encoding/json"
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

// TestAcquireUnavailableRetryDefault verifies the post-2026-05-23
// behavior: an explicit `error_types: { "acquire/unavailable": {
// policy: [retry] } }` declaration drives the node to retry on
// Unavailable. The stub starts with an empty queue (Open returns
// Unavailable). After observing at least one Open against the empty
// queue, we seed an item and confirm the node eventually reaches fresh
// on a subsequent retry.
func TestAcquireUnavailableRetryDefault(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
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
	h.Stub.WhenType("worker").Success(map[string]any{"ok": 1}, true, "ran")

	// @deliberate: Post-2026-05-23: explicit retry opt-in via error_types: {
	// "acquire/unavailable": { policy: [retry × N] } }. Without this
	// the default is fail-fast.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-default", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{
								// @deliberate: Allow many retries so the test has
								// time to seed the queue before a chain
								// exhaustion would land in failed.
								{Action: "retry", Count: 1000, BaseDelayMs: 100},
								{Action: "give_up"},
							},
						},
					},
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-default", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// @deliberate: Wait until the producer has observed at least one Open against
	// the empty queue. This pins that the supervisor probed the producer
	// and got Unavailable.
	deadline := time.Now().Add(10 * time.Second)
	var sawFirstOpen bool
	for time.Now().Before(deadline) {
		for _, c := range sub.Calls() {
			if c.Verb == "open" {
				sawFirstOpen = true
				break
			}
		}
		if sawFirstOpen {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, sawFirstOpen, "stub producer should have seen at least one Open against the empty queue")

	// @deliberate: Confirm the node has NOT transitioned to fresh — the explicit
	// retry chain should keep it stale (no settling_signal_type should
	// be set yet; the retry hasn't terminated).
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.NotEqual(t, cascade.NodeStateFresh, wRow.State,
		"silent-retry must NOT transition the node on Unavailable")

	// @deliberate: Seed an item — the next scheduler tick should pick it up.
	_, err := sub.SeedPickPolicyItem("@queue", json.RawMessage(`{"v":1}`))
	require.NoError(t, err, "seed item")

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker did not reach fresh after seeding the queue (silent-retry default)")
}
