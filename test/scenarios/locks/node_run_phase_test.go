// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.


package locks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestNodeRunPhaseAdvancesOnClaim(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "phase-advances", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("content", "/region-phase")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-phase-advances", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")
	require.True(t, h.WaitForWorkerRequestDeleted(n.ID, 5*time.Second),
		"node-run row must be deleted at terminal (phase='completed' equivalent)")
}

func TestClaimHandleIsHeldColumnPopulated(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario").Delay(2 * time.Second)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "is-held-column", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("content", "/region-is-held")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-is-held-column", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	deadline := time.Now().Add(10 * time.Second)
	var found bool
	var isHeld bool
	for time.Now().Before(deadline) {
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT is_held FROM rimsky_claim_handles WHERE holder_node_id = $1 AND lock_kind = 'claim_scope'`,
			n.ID,
		).Scan(&isHeld)
		if err == nil {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, found, "claim_handle row not observed within deadline")
	require.False(t, isHeld,
		"non-held single-node template's claim_handle must carry is_held=false")
}
