// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestClaimScopeClaimRace_OneAcquirerWins(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "scope-race", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithClaimProducers(scenario.WriteClaimRef("content", "/contended")),
			),
		},
	})
	iidA := h.CreateInstance(tid, "ck-scope-race-A", map[string]any{})
	iidB := h.CreateInstance(tid, "ck-scope-race-B", map[string]any{})

	wA := h.FindNode(iidA, "worker")
	wB := h.FindNode(iidB, "worker")
	require.NotNil(t, wA)
	require.NotNil(t, wB)
	h.WaitForDispatchCount(wA.ID, 1)
	h.WaitForDispatchCount(wB.ID, 1)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	dialCtx, dialCancel := context.WithTimeout(h.Ctx, 5*time.Second)
	defer dialCancel()
	client, err := peer.Dial(dialCtx, "content", "grpc://"+endpoint, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	reg := locks.NewRegistry()
	reg.Add("content", client)

	makeArgs := func(supID string) runtime.RunArgs {
		return runtime.RunArgs{
			Persist:               h.Persist,
			Queue:                 h.Queue,
			ClaimHandles:          h.Persist.ClaimHandles(),
			AdvisoryLocker:        h.Driver.AdvisoryLocker(),
			ClaimProducerRegistry: reg,
			Clock:                 shared.SystemClock{},
			Logger:                shared.SilentLogger{},
			SupervisorID:          supID,
			Pool:                  pool,
			Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
				"stub": {Transport: "grpc", URL: h.StubAddr},
			}),
			LivenessInterval: 100 * time.Millisecond,
		}
	}

	type result struct {
		supID string
		out   runtime.RunnerResult
		err   error
	}

	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup

	for _, supID := range []string{"sup-A", "sup-B"} {
		wg.Add(1)
		args := makeArgs(supID)
		go func(id string, a runtime.RunArgs) {
			defer wg.Done()
			<-start
			out, err := runtime.RunNode(h.Ctx, a, nil)
			results <- result{supID: id, out: out, err: err}
		}(supID, args)
	}
	close(start)
	wg.Wait()
	close(results)

	var rA, rB result
	for r := range results {
		if r.supID == "sup-A" {
			rA = r
		} else {
			rB = r
		}
	}

	require.NoError(t, rA.err, "sup-A unexpected error")
	require.NoError(t, rB.err, "sup-B unexpected error")

	wins := 0
	if rA.out.Ran {
		wins++
	}
	if rB.out.Ran {
		wins++
	}
	require.GreaterOrEqual(t, wins, 1, "at least one supervisor must successfully acquire")

	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles
		  WHERE producer_name = $1 AND lock_kind = 'claim_scope'
		    AND state = 'active'`,
		"content",
	).Scan(&lhCount))
	require.LessOrEqual(t, lhCount, 1,
		"at most one ACTIVE writer-scope lock-holder row per (store, scope)")

	_ = iidA
	_ = iidB
}

func waitForActiveClaimScopeCount(t *testing.T, h *scenario.Harness, producerName string, want int) {
	t.Helper()
	for {
		var n int
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_claim_handles
			  WHERE producer_name = $1 AND lock_kind = 'claim_scope' AND state = 'active'`,
			producerName,
		).Scan(&n))
		if n >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestClaimScope_DisjointScopesCoexist(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	h.Stub.WhenType("worker-a").HoldUntil(releaseA).Success(map[string]any{}, true, "a")
	h.Stub.WhenType("worker-b").HoldUntil(releaseB).Success(map[string]any{}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "disjoint-scope-coexist", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker-a", Executor: "stub"},
				scenario.WithClaimProducers(scenario.WriteClaimRef("content", "/scope-a")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker-b", Executor: "stub"},
				scenario.WithClaimProducers(scenario.WriteClaimRef("content", "/scope-b")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-disjoint-scope-coexist", map[string]any{})

	wA := h.FindNode(iid, "worker-a")
	wB := h.FindNode(iid, "worker-b")
	require.NotNil(t, wA)
	require.NotNil(t, wB)
	h.WaitForDispatchCount(wA.ID, 1)
	h.WaitForDispatchCount(wB.ID, 1)

	waitForActiveClaimScopeCount(t, h, "content", 2)

	var distinctScopes int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(DISTINCT claim_scope_data) FROM rimsky_claim_handles
		  WHERE producer_name = 'content' AND lock_kind = 'claim_scope' AND state = 'active'`,
	).Scan(&distinctScopes))
	require.Equal(t, 2, distinctScopes,
		"disjoint (byte-unequal) scopes must hold two distinct ACTIVE claim-scope rows concurrently")

	close(releaseA)
	close(releaseB)
	h.WaitForNodeState(wA.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(wB.ID, cascade.NodeStateFresh)
}
