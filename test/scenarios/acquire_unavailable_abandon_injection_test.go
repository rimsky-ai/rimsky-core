// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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

	endpointB, storeB, teardownB := stubfixture.Start(t, stubstore.Config{
		Capabilities: syncCaps,
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
			},
		},
	})
	t.Cleanup(teardownB)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"store-a": {Endpoint: "grpc://" + endpointA, Capabilities: syncCaps},
				"store-b": {Endpoint: "grpc://" + endpointB, Capabilities: syncCaps},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-abandon-injection", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Action: "give_up",
						},
					},
				},
				scenario.WithClaimProducers(
					scenario.WriteClaimRef("store-a", "@items"),
					scenario.WriteClaimRef("store-b", "@queue"),
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-abandon", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	h.WaitForDispatch(worker.ID)

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

	var hooked atomic.Bool
	args := runtime.RunArgs{
		Persist:                h.Persist,
		Queue:                  h.Queue,
		ClaimHandles:           h.Persist.ClaimHandles(),
		AdvisoryLocker:         h.Driver.AdvisoryLocker(),
		StoreRegistry:          registry,
		Clock:                  shared.SystemClock{},
		Logger:                 shared.SilentLogger{},
		SupervisorID:           "scenario-runner",
		AcceptedExecutors:      []string{"stub"},
		AcceptedClaimProducers: []string{"store-a", "store-b"},
		Pool:                   pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		LivenessInterval: 100 * time.Millisecond,
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

	_, ferr := runtime.FlushProducerVerbOutbox(h.Ctx, args)
	require.NoError(t, ferr)

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

	callsB := storeB.Calls()
	require.Equal(t, 1, countCalls(callsB, "open"),
		"store-b must have been Open'd exactly once")
	require.Zero(t, countCalls(callsB, "abandon"),
		"store-b never acquired — no Abandon may fire against it")

	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Zero(t, lhCount, "no claim-handle rows may survive the abandoned acquisition")

	var failedLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, gerr := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		failedLatest = r
		return gerr
	}))
	require.NotNil(t, failedLatest)
	require.Equal(t, cascade.NodeStateFailed, failedLatest.State,
		"give_up on acquire/unavailable must land the node in failed")

	var sigCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE node_id = $1 AND kind = 'terminal/error/acquire/unavailable'`,
		worker.ID,
	).Scan(&sigCount))
	require.Equal(t, 1, sigCount,
		"the error path must emit exactly one terminal/error/acquire/unavailable signal")

	require.Empty(t, h.Stub.Observed(),
		"the executor must not be invoked when acquisition fails on Unavailable")
}
