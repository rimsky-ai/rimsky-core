// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

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

func TestAcquireUnavailablePass(t *testing.T) {
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
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-pass", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Action: "pass",
						},
					},
				},
				scenario.WithClaimProducers(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/")

	var wLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		wLatest = r
		return err
	}))
	require.NotNil(t, wLatest)
	require.Equal(t, cascade.NodeStateFresh, wLatest.State,
		"worker should be fresh after resolve=pass")

	var sawOpen bool
	for _, c := range sub.Calls() {
		if c.Verb == "open" {
			sawOpen = true
			break
		}
	}
	require.True(t, sawOpen, "stub producer should have received at least one Open")

	require.Empty(t, h.Stub.Observed(),
		"executor must not be invoked when error_types: { acquire/unavailable: [pass] } fires")
}
