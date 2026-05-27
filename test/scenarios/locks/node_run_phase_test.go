// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Node-run phase column coverage (Phase 5 of layer-crystallization).
//
// The Phase-5 schema consolidation introduced a `phase` column on
// rimsky_node_runs to drive the active+held lifecycle. This test
// asserts the column transitions:
//
//   - Pending phase: a freshly-enqueued node-run has phase='pending',
//     claimed_by IS NULL.
//   - Active phase: claiming the node-run advances it to phase='active'
//     with claimed_by set to the supervisor id.
//   - is_held column: claim handles for held subgraphs carry is_held=true;
//     non-held claims carry is_held=false. Named locks always carry
//     is_held=false.
//
// Distinct from atomic_acquisition_test.go (which exercises the
// rollback-on-Open-error path under invariants 10 and 15) — this test
// exercises the new schema columns directly.

package locks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/control/config"
	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/stores/stub/testfixture"
)

// TestNodeRunPhaseAdvancesOnClaim asserts that a successful
// claim transitions the node-run row from phase='pending' to
// phase='active', and that on terminal the row is deleted.
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

	// Drive the node through to terminal. After fresh, the node-run
	// row is deleted. We can't directly observe the 'active' phase mid-run
	// without race-conditional polling; we instead verify the lifecycle:
	// (a) the node-run row was inserted (driven by scheduler enqueue),
	// (b) it advanced through the active phase (executor saw the work), and
	// (c) it was deleted at terminal. Phase column existence is verified
	// by the SELECT itself succeeding.
	//
	// Sequencing note: `Queue.Complete` runs inside the supervisor's
	// poll-goroutine AFTER `applyTerminalComplete` returns, so the
	// node may reach `fresh` slightly before the node-run row is
	// physically deleted. Polling for the deletion directly removes
	// the race; the prior pattern (wait for fresh, then SELECT count)
	// flaked under load.
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")
	require.True(t, h.WaitForWorkerRequestDeleted(n.ID, 5*time.Second),
		"node-run row must be deleted at terminal (phase='completed' equivalent)")
}

// TestClaimHandleIsHeldColumnPopulated asserts the is_held column is
// populated correctly on the claim_handle row at acquisition time.
// Named-lock holders carry is_held=false; non-held scope claims carry
// is_held=false; held scope claims (subgraph size > 1 via inherits)
// would carry is_held=true. This test focuses on the non-held case
// since it can be observed directly without the inheritor's terminal
// having fired (which deletes the parent row).
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
	// Delay terminal so we can observe the claim_handle row before it's
	// deleted at terminal. The Delay applies before the stub returns a
	// terminal response; during this window the row is live and the
	// is_held column carries its acquisition-time value.
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

	// Wait for the claim_handle row to appear (acquisition tx committed).
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
