// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 27 — default behavior under Unavailable: silent retry. Without
// any on_acquire_unavailable handler, an Unavailable response from the
// producer must NOT transition the node — the next scheduler tick
// retries. Verified by seeding an item into the empty queue mid-run
// and confirming the node ultimately reaches fresh.
package scenarios

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

// TestAcquireUnavailableRetryDefault verifies the today-behavior default:
// no on_acquire_unavailable handler → silent retry. The stub starts
// with an empty queue (Open returns Unavailable). After observing at
// least one Open against the empty queue, we seed an item and confirm
// the node eventually reaches fresh on a subsequent retry.
func TestAcquireUnavailableRetryDefault(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommitDefault: "delete",
				OnGiveUpDefault: "release_to_back",
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Complete(map[string]any{"ok": 1}, true, "ran")

	// Template has no on_acquire_unavailable handler — silent-retry default.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-default", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-default", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait until the producer has observed at least one Open against
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

	// Confirm the node has NOT transitioned to fresh — silent retry should
	// keep it stale (no last_outcome should be set yet).
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.NotEqual(t, shared.NodeStateFresh, wRow.State,
		"silent-retry must NOT transition the node on Unavailable")

	// Seed an item — the next scheduler tick should pick it up.
	_, err := sub.SeedPickPolicyItem("@queue", json.RawMessage(`{"v":1}`))
	require.NoError(t, err, "seed item")

	// The node should now reach fresh via the silent retry.
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 30*time.Second),
		"worker did not reach fresh after seeding the queue (silent-retry default)")
}
