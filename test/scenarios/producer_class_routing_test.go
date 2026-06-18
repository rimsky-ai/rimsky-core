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
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

const producerClassUnavailable = "pg/claim_unavailable"

func startClassifyingProducer(t *testing.T) (*scenario.Harness, *stubstore.Store) {
	t.Helper()
	caps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		DeclaredErrorClasses:  []string{producerClassUnavailable},
	}
	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: caps,
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit:         action.Action{Kind: action.Pop},
				OnGiveUp:         action.Action{Kind: action.Recycle},
				UnavailableClass: producerClassUnavailable,
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: caps,
				},
			},
		},
	})
	return h, sub
}

func driveProducerClassifiedRetry(
	t *testing.T, h *scenario.Harness, sub *stubstore.Store,
	templateName, contextKey string, errorTypes map[string]node.ErrorTypePolicy,
) {
	t.Helper()
	h.Stub.WhenType("worker").Success(map[string]any{"ok": 1}, true, "ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: templateName, Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:       "worker",
					Executor:   "stub",
					ErrorTypes: errorTypes,
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	for _, w := range h.LastDeployWarnings {
		require.NotContains(t, w, producerClassUnavailable,
			"registration must not warn about the producer-declared class %q — "+
				"a warning means the producer vocabulary never reached the validator", producerClassUnavailable)
	}
	iid := h.CreateInstance(tid, contextKey, map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t,
		h.WaitForEventKind(worker.ID, "transient/retry/1/"+producerClassUnavailable, 30*time.Second),
		"retry action must emit transient/retry/1/%s on the event log", producerClassUnavailable)

	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.NotNil(t, wRow)
	require.NotEqual(t, cascade.NodeStateFailed, wRow.State,
		"retry policy must hold the node; failed means the producer class did not route")

	_, err := sub.SeedPickPolicyItem("@queue", json.RawMessage(`{"v":1}`))
	require.NoError(t, err, "seed item")
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should reach fresh after seeding the queue under the retry chain")
}

func TestProducerClassRouting_ExactMatch(t *testing.T) {
	t.Parallel()
	h, sub := startClassifyingProducer(t)
	driveProducerClassifiedRetry(t, h, sub,
		"producer-class-exact", "ck-producer-class-exact",
		map[string]node.ErrorTypePolicy{
			producerClassUnavailable: {
				Policy: []node.PolicyAction{
					{Action: "retry", Count: 1000, BaseDelayMs: 100},
					{Action: "give_up"},
				},
			},
		})
}

func TestProducerClassRouting_PrefixFallback(t *testing.T) {
	t.Parallel()
	h, sub := startClassifyingProducer(t)
	driveProducerClassifiedRetry(t, h, sub,
		"producer-class-fallback", "ck-producer-class-fallback",
		map[string]node.ErrorTypePolicy{
			"acquire/unavailable": {
				Policy: []node.PolicyAction{
					{Action: "retry", Count: 1000, BaseDelayMs: 100},
					{Action: "give_up"},
				},
			},
		})
}
