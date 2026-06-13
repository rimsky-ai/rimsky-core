// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Deterministic injection test for the acquire-unavailable abandon
// path (`runner_lifecycle.go::handleAcquireUnavailable`).
//
// A node requires two claims. Producer #1 (store-a) Opens successfully;
// producer #2 (store-b) returns the Unavailable sentinel. The
// acquisition tx rolls back via errAcquireUnavailable, carrying the
// partially-Open'd store-a claim in acq.PartialLocks, and
// handleAcquireUnavailable must Abandon it exactly once.
//
// The race-shaped property under test is the post-rollback /
// pre-Abandon window: the tx-side claim-handle rows are already gone
// while the producer-side Abandon has not yet fired. Forcing that
// window deterministically requires an injection point between the
// rollback and abandonPartialLocks — `RunArgs.PreAcquireUnavailableHook`,
// a nil-default test-only seam. The hook observes (a) zero surviving
// claim-handle rows and (b) zero Abandons received by producer #1 so
// far; after RunNode returns, producer #1 must have received exactly
// one Abandon for the claim it Open'd, and the run must have resolved
// through the error path with the synthetic class acquire/unavailable.
package scenarios

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// countCalls returns how many recorded stub-store calls match verb.
func countCalls(calls []stubstore.Call, verb string) int {
	n := 0
	for _, c := range calls {
		if c.Verb == verb {
			n++
		}
	}
	return n
}

func TestAcquireUnavailable_AbandonsPartialOpensExactlyOnce(t *testing.T) {
	t.Parallel()

	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}

	// Producer #1: one seeded item, so Open succeeds. The recycle-on-
	// give-up action is irrelevant to the assertion; the Calls recorder
	// is what counts the Abandon.
	endpointA, storeA, teardownA := stubfixture.Start(t, stubstore.Config{
		Capabilities: syncCaps,
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@items": {
				OnCommit:     action.Action{Kind: action.Pop},
				OnGiveUp:     action.Action{Kind: action.Recycle},
				InitialItems: []json.RawMessage{json.RawMessage(`{"k":"v"}`)},
			},
		},
	})
	t.Cleanup(teardownA)

	// Producer #2: empty queue — Open returns Unavailable. Real store
	// service over the wire: the defense under test (the partial-open
	// Abandon) is NOT stubbed out; the Unavailable comes from the
	// producer's genuine empty-queue branch.
	endpointB, storeB, teardownB := stubfixture.Start(t, stubstore.Config{
		Capabilities: syncCaps,
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// No InitialItems — Open returns Unavailable.
			},
		},
	})
	t.Cleanup(teardownB)

	// NoSupervisor: the test drives runtime.RunNode directly so it can
	// thread the injection hook. The scheduler still runs and enqueues
	// the root dispatch row.
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"store-a": {Endpoint: "grpc://" + endpointA, Capabilities: syncCaps},
				"store-b": {Endpoint: "grpc://" + endpointB, Capabilities: syncCaps},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-abandon-injection", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				// Sorted acquisition order (@blessed-invariant 3) is
				// (lock_kind, producer:selector): "store-a:@items" <
				// "store-b:@queue", so store-a Opens first and store-b's
				// Unavailable leaves store-a partially open.
				scenario.WithStores(
					scenario.WriteClaimRef("store-a", "@items"),
					scenario.WriteClaimRef("store-b", "@queue"),
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-abandon", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.True(t, h.WaitForDispatch(worker.ID, 10*time.Second),
		"scheduler should enqueue the worker's dispatch row")

	// Dial real gRPC producer clients — the same client type the
	// production supervisor registers.
	clientA, err := peer.Dial(h.Ctx, "store-a", "grpc://"+endpointA, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(clientA.Close)
	clientB, err := peer.Dial(h.Ctx, "store-b", "grpc://"+endpointB, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(clientB.Close)
	registry := locks.NewRegistry()
	registry.Add("store-a", clientA)
	registry.Add("store-b", clientB)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// hooked records whether the injection seam actually fired —
	// guards against a silent regression where handleAcquireUnavailable
	// stops invoking the hook (which would make the post-rollback /
	// pre-Abandon assertions vacuous).
	var hooked atomic.Bool
	args := runtime.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		ClaimHandles:      h.Persist.ClaimHandles(),
		AdvisoryLocker:    h.Driver.AdvisoryLocker(),
		StoreRegistry:     registry,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "scenario-runner",
		AcceptedExecutors: []string{"stub"},
		AcceptedStores:    []string{"store-a", "store-b"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
		// The post-rollback / pre-Abandon window: the acquisition tx is
		// rolled back (no claim-handle rows survive) but producer #1 has
		// not yet received its Abandon.
		PreAcquireUnavailableHook: func(ctx context.Context) {
			require.Zero(t, countCalls(storeA.Calls(), "abandon"),
				"at hook time the partial open's Abandon must not have fired yet")
			var lhCount int
			require.NoError(t, h.Pool.QueryRow(ctx,
				`SELECT count(*) FROM rimsky_claim_handles lh
				   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
				  WHERE n.instance_id = $1`, uuid.UUID(iid),
			).Scan(&lhCount))
			require.Zero(t, lhCount,
				"at hook time the acquisition tx must already be rolled back — no claim-handle rows survive")
			hooked.Store(true)
		},
	}

	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.False(t, out.Ran,
		"the candidate must not dispatch — the Unavailable routed it through the error path")
	require.True(t, hooked.Load(),
		"the PreAcquireUnavailableHook seam must have fired")

	// Producer #1 received exactly one Open and exactly one Abandon,
	// and the Abandon targets the claim the Open minted.
	callsA := storeA.Calls()
	require.Equal(t, 1, countCalls(callsA, "open"),
		"store-a must have been Open'd exactly once")
	require.Equal(t, 1, countCalls(callsA, "abandon"),
		"store-a must receive exactly one Abandon for its partial open")
	var openClaimID, abandonClaimID string
	for _, c := range callsA {
		switch c.Verb {
		case "open":
			openClaimID = c.ClaimID
		case "abandon":
			abandonClaimID = c.ClaimID
		}
	}
	require.Equal(t, openClaimID, abandonClaimID,
		"the Abandon must target the claim the Open minted")
	require.Zero(t, countCalls(callsA, "commit"),
		"store-a must never see a Commit for an abandoned partial open")

	// Producer #2 saw the Open that returned Unavailable and nothing else.
	callsB := storeB.Calls()
	require.Equal(t, 1, countCalls(callsB, "open"),
		"store-b must have been Open'd exactly once")
	require.Zero(t, countCalls(callsB, "abandon"),
		"store-b never acquired — no Abandon may fire against it")

	// No claim-handle rows survive.
	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Zero(t, lhCount, "no claim-handle rows may survive the abandoned acquisition")

	// The run resolved through the error path with the synthetic class:
	// give_up lands the node in failed with a
	// terminal/error/acquire/unavailable signal row on the event log.
	var failedRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, gerr := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		failedRow = r
		return gerr
	}))
	require.Equal(t, cascade.NodeStateFailed, failedRow.State,
		"give_up on acquire/unavailable must land the node in failed")

	var sigCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE node_id = $1 AND kind = 'terminal/error/acquire/unavailable'`,
		worker.ID,
	).Scan(&sigCount))
	require.Equal(t, 1, sigCount,
		"the error path must emit exactly one terminal/error/acquire/unavailable signal")

	// Executor never invoked.
	require.Empty(t, h.Stub.Observed(),
		"the executor must not be invoked when acquisition fails on Unavailable")
}
