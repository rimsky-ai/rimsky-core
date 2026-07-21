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

func TestAcquireUnavailable_ExplicitRetryAction(t *testing.T) {
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
		Name: "acq-unavail-explicit-retry", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:         "worker",
					Executor:     "stub",
					MaxRetries:   node.IntPtr(1000),
					RetryBackoff: &node.RetryBackoffConfig{BaseDelayMs: 100},
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {Action: "retry"},
					},
				},
				scenario.WithClaimProducers(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-explicit-retry", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	deadline := time.Now().Add(10 * time.Second)
	var sawFirstOpen, sawRun bool
	for time.Now().Before(deadline) {
		for _, c := range sub.Calls() {
			if c.Verb == "open" {
				sawFirstOpen = true
				break
			}
		}
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
			if err != nil {
				return err
			}
			if r != nil {
				sawRun = true
				require.NotEqual(t, cascade.NodeStateFresh, r.State,
					"silent-retry must NOT transition the node on Unavailable")
			}
			return nil
		}))
		if sawFirstOpen && sawRun {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, sawFirstOpen, "stub producer should have seen at least one Open against the empty queue")
	require.True(t, sawRun, "expected a node run row while silent retries are in flight")

	_, err := sub.SeedPickPolicyItem("@queue", json.RawMessage(`{"v":1}`))
	require.NoError(t, err, "seed item")

	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)
}
