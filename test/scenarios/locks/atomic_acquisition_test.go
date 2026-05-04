// Atomic-acquisition scenario coverage — invariants 10 and 15.
//
// Invariant 10 (rimsky-side, v3 §4.10): the §7.3 acquisition transaction
// either claims dispatch AND inserts every required `rimsky_claim_handle`
// row AND records the `Store.Open`-returned address, or none of these.
// The store's own state mutations run in a decoupled tx; rimsky-side
// atomicity is independent.
//
// Invariant 15 (revised v3): `Open` fires inside the rimsky-side
// acquisition transaction. When `Open` errors, the rimsky-side INSERTs
// must roll back so single-writer-per-scope (4b) is not violated by an
// orphan lock-holder row.
//
// Two tests:
//   - TestAtomicAcquisitionRollsBackOnOpenError exercises the rollback
//     path using `core/store/storetest.Fake` directly. The fake's
//     in-process surface is sufficient because the rollback is a
//     rimsky-side property — wire-roundtrip behaviour adds no additional
//     coverage of invariant 10's all-or-nothing INSERT semantics.
//   - TestLockHolderRowDeletedAfterTerminal complements the loopback wire
//     coverage in stores/regional_claim_test.go by also asserting the
//     post-terminal `rimsky_claim_handle` row count is zero — invariant
//     4 (claimant-guarded release) end-to-end.
package locks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/locks/storetest"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/executor"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

// TestAtomicAcquisitionRollsBackOnOpenError seeds a one-node template
// whose claim is backed by a `storetest.Fake` configured to error on
// every `open` call. The supervisor's per-candidate acquisition tx
// must roll back: zero lock-holder rows for the node and the dispatch
// row's `claimed_by` reverts to NULL.
//
// Uses the in-Go fake (registered into the runner's RunArgs directly)
// instead of an error-injecting wire fixture because the property
// under test is rimsky-side rollback semantics — the store
// success path is irrelevant. The wire-bridged happy path is covered in
// stores/regional_claim_test.go. The harness still wires a loopback stub
// fixture into the control-api / scheduler so template deploy and
// candidate enqueue work; the supervisor (NoSupervisor=true) is replaced
// by a hand-built RunArgs whose StoreRegistry holds the error-injecting
// Fake under the same name.
func TestAtomicAcquisitionRollsBackOnOpenError(t *testing.T) {
	t.Parallel()

	// Loopback stub for control-api and scheduler startup. The Fake
	// shadows it inside the runner-local registry built below.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "open-error", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("content", "/region-A")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-open-error", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForDispatch(n.ID, 5*time.Second),
		"expected scheduler to enqueue a dispatch row")

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// Build a runner-local registry with the error-injecting Fake. This
	// registry shadows the harness control-api's registry — the runner
	// uses what we hand it via RunArgs.
	fake := storetest.NewFake("content", locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	openErr := errOpenInjected{}
	fake.ErrorFunc = func(verb string, _ locks.ClaimID) error {
		if verb == "open" {
			return openErr
		}
		return nil
	}
	reg := locks.NewRegistry()
	reg.Add("content", fake)

	args := integration.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		LockHolders:       h.Persist.LockHolders(),
		AdvisoryLocker:    h.Driver.AdvisoryLocker(),
		StoreRegistry:     reg,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "scenario-runner-rollback",
		AcceptedExecutors: []string{"stub"},
		AcceptedStores:    []string{"content"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
	}
	out, err := integration.RunNode(h.Ctx, args, nil)
	// Open errors surface as the RunNode error (the per-candidate tx
	// rolls back deferred-style). The load-bearing assertion is that
	// the rollback actually happened — see the row-count checks below.
	require.Error(t, err, "Open error must surface")
	require.False(t, out.Ran,
		"acquisition tx must roll back when Open errors; runner advertises Ran=false")

	// Invariant 10 (rimsky-side): zero lock-holder rows for the node.
	var lhCount int
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handle WHERE holder_node_id = $1`, n.ID,
	).Scan(&lhCount)
	require.NoError(t, err)
	require.Equal(t, 0, lhCount, "rollback must leave no rimsky_claim_handle rows")

	// Invariant 10 (rimsky-side): dispatch row's claimed_by is NULL again.
	var claimedBy *string
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT claimed_by FROM rimsky_worker_request WHERE node_id = $1`, n.ID,
	).Scan(&claimedBy)
	require.NoError(t, err)
	require.Nil(t, claimedBy, "rollback must release the dispatch claim")

	// The Fake observed exactly one open attempt (the supervisor tried,
	// got the injected error, and rolled back).
	calls := fake.Calls()
	openCount := 0
	for _, c := range calls {
		if c.Verb == "open" {
			openCount++
		}
	}
	require.Equal(t, 1, openCount,
		"Open should fire exactly once before the error rolls back the tx")
}

// errOpenInjected is the canary error the Fake's ErrorFunc returns for
// the open verb in TestAtomicAcquisitionRollsBackOnOpenError.
type errOpenInjected struct{}

func (errOpenInjected) Error() string { return "injected open error" }

// TestLockHolderRowDeletedAfterTerminal drives one scope claim
// through the loopback gRPC fixture and asserts that after the worker
// reaches `fresh`, zero `rimsky_claim_handle` rows remain for the node.
// Complements stores/regional_claim_test.go by adding the post-terminal
// row-count assertion — invariant 4 (claimant-guarded release) end to
// end through the §7.3 atomic path.
func TestLockHolderRowDeletedAfterTerminal(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "scenario")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "release-after-terminal", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("content", "/region-B")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-release-after-terminal", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	// Invariant 4 / 10: post-terminal lock-holder row count is zero
	// (the supervisor's claimant-guarded release deleted it).
	deadline := time.Now().Add(2 * time.Second)
	var lhCount int
	for time.Now().Before(deadline) {
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_claim_handle WHERE holder_node_id = $1`, n.ID,
		).Scan(&lhCount)
		require.NoError(t, err)
		if lhCount == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, 0, lhCount,
		"after worker reaches fresh, zero lock-holder rows must remain (invariant 4)")
}
