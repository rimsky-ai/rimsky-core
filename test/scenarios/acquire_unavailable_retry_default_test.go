// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": 1}, true, "ran")

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
								{Action: "retry", Count: 1000, BaseDelayMs: 100},
								{Action: "give_up"},
							},
						},
					},
				},
				scenario.WithClaimProducers(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-default", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

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

	var wLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		wLatest = r
		return err
	}))
	if wLatest != nil {
		require.NotEqual(t, cascade.NodeStateFresh, wLatest.State,
			"silent-retry must NOT transition the node on Unavailable")
	}

	_, err := sub.SeedPickPolicyItem("@queue", json.RawMessage(`{"v":1}`))
	require.NoError(t, err, "seed item")

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker did not reach fresh after seeding the queue (silent-retry default)")
}
