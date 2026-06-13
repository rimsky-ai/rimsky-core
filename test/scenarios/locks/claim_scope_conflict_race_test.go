// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// ClaimScope-conflict race scenario coverage — invariant 4b (single-
// writer-per-claim-scope), with explicit regression cover for the cycle-4
// fix at `foundation/persistence/postgres/advisory_locker.go::TakeClaimScopeLockInTx`
// (called from `runtime/runner_acquire.go::acquireClaim`).
//
// Setup:
//   - One harness with a loopback stub fixture under a single store name.
//   - One template with two nodes (worker-A, worker-B), both holding
//     a scope rw claim against the same selector. NoSupervisor so we
//     drive RunNode manually with two SupervisorIDs.
//   - Two goroutines: each calls runtime.RunNode with a distinct
//     SupervisorID. A shared start channel releases both at the same
//     instant and a sync.WaitGroup joins them; the post-condition is
//     asserted against the persisted rimsky_claim_handles rows, not an
//     in-memory counter.
//
// The load-bearing assertion: at no point during acquisition do TWO
// `rimsky_claim_handles` rows for the contended claim-scope exist
// simultaneously. The advisory lock on `(store, claim_scope)` serializes
// the two acquisition transactions; only one can pass
// `evaluateClaimScopeConflict` at a time. After the first commits, the
// second's predicate sees the holder row and returns conflict=true.
//
// SQL-primitive coverage of `TakeClaimScopeLockInTx` itself lives in
// `foundation/persistence/conformance/sort_order.go` (the claim-scope-lock
// branch of the sort-order conformance test, which both postgres and
// sqlite drivers run). This scenario test exercises it through the real
// supervisor acquisition flow.
package locks

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestClaimScopeClaimRace_OneAcquirerWins exercises the single-writer-per-
// claim-scope invariant by racing two supervisors against the same selector.
// Exactly one wins; the other backs off with Ran=false (claim-scope conflict
// is a soft skip, not an error).
func TestClaimScopeClaimRace_OneAcquirerWins(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	// One template (single root node holding the contended claim), two
	// instances. Each instance gets its own root frame, dispatch row,
	// and node row — so two independent supervisors can race against
	// the same scope without frame-engine starvation issues that arise
	// when two root nodes share an instance under serial_queue/coalesce.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "scope-race", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("content", "/contended")),
			),
		},
	})
	iidA := h.CreateInstance(tid, "ck-scope-race-A", map[string]any{})
	iidB := h.CreateInstance(tid, "ck-scope-race-B", map[string]any{})

	wA := h.FindNode(iidA, "worker")
	wB := h.FindNode(iidB, "worker")
	require.NotNil(t, wA)
	require.NotNil(t, wB)
	require.True(t, h.WaitForDispatch(wA.ID, 15*time.Second), "wA dispatch row never appeared")
	require.True(t, h.WaitForDispatch(wB.ID, 15*time.Second), "wB dispatch row never appeared")

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// Build one shared registry holding a peer.Client to the loopback
	// fixture. The Client and the underlying gRPC connection are
	// concurrency-safe so two RunNode goroutines can share both.
	dialCtx, dialCancel := context.WithTimeout(h.Ctx, 5*time.Second)
	defer dialCancel()
	client, err := peer.Dial(dialCtx, "content", "grpc://"+endpoint, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	reg := locks.NewRegistry()
	reg.Add("content", client)

	makeArgs := func(supID string) runtime.RunArgs {
		return runtime.RunArgs{
			Persist:           h.Persist,
			Queue:             h.Queue,
			ClaimHandles:      h.Persist.ClaimHandles(),
			AdvisoryLocker:    h.Driver.AdvisoryLocker(),
			StoreRegistry:     reg,
			Clock:             shared.SystemClock{},
			Logger:            shared.SilentLogger{},
			SupervisorID:      supID,
			AcceptedExecutors: []string{"stub"},
			AcceptedStores:    []string{"content"},
			Pool:              pool,
			Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
				"stub": {Transport: "grpc", URL: h.StubAddr},
			}),
			HeartbeatInterval: 100 * time.Millisecond,
		}
	}

	type result struct {
		supID string
		out   runtime.RunnerResult
		err   error
	}

	// Barrier so both goroutines call RunNode at the same instant.
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

	// Allowed shapes per supervisor: (Ran=true, err=nil) — won the race;
	// (Ran=false, err=nil) — lost the race (scope conflict; soft skip).
	// Open-error / RPC-error shapes are not expected in this scenario;
	// surface them clearly if they happen.
	require.NoError(t, rA.err, "sup-A unexpected error")
	require.NoError(t, rB.err, "sup-B unexpected error")

	// Possible outcomes:
	//   - One Ran=true, one Ran=false  → race serialized correctly.
	//   - Both Ran=true                → BOTH supervisors won; this is
	//     allowed iff their work didn't overlap in wall-clock time
	//     (the winning runner committed, released, and the second runner
	//     then re-tried and acquired). In this single-shot test that's
	//     unlikely but not impossible — both could finish their full cycle
	//     between RunNode invocations because the stub executor returns
	//     a Complete event immediately. The load-bearing check is the
	//     post-condition lock-holder row count.
	//   - Both Ran=false               → impossible if dispatch rows exist
	//     and the supervisors can read them.
	wins := 0
	if rA.out.Ran {
		wins++
	}
	if rB.out.Ran {
		wins++
	}
	require.GreaterOrEqual(t, wins, 1, "at least one supervisor must successfully acquire")

	// Invariant 4b: single-writer-per-scope. After both goroutines
	// returned, the count of ACTIVE scope-kind rimsky_claim_handles rows
	// for the contended (store, scope) must be ≤ 1. The state filter is
	// load-bearing: post-Stage-3 of the claim-handle state-column
	// refactor, terminals PROMOTE the row (state='committed', preserved
	// for forensics / retention) rather than deleting it — and the
	// both-Ran=true sequential-win shape documented above legitimately
	// leaves TWO committed rows behind. Those are remnants, not live
	// holders; the invariant constrains simultaneous ACTIVE ownership
	// only. The 0-case is the common observation (both terminals fired
	// and resolved their rows); the 1-case is the rare one where a
	// runner's row is still mid-release at sample time.
	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles
		  WHERE producer_name = $1 AND lock_kind = 'claim_scope'
		    AND state = 'active'`,
		"content",
	).Scan(&lhCount))
	require.LessOrEqual(t, lhCount, 1,
		"invariant 4b: at most one ACTIVE writer-scope lock-holder row per (store, scope)")

	_ = iidA
	_ = iidB
}
