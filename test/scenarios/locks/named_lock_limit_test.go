// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Named-lock capacity-limit behavioral coverage — the named-lock concept's
// mutex (limit:1) / semaphore (limit:N) enforcement and the
// increment-at-acquire / decrement-at-terminal disposition
// (@blessed-invariant 2).
//
// The deterministic-ordering invariant (blessed-invariant 3) is already
// covered by the deadlock-guard SQL test + the SortOrderCoordination
// conformance, and deploy-time limit validation by control-api's
// app_test.go. What had NO behavioral coverage before this file was the
// actual capacity enforcement: that at most `limit` node-runs may hold a
// named lock concurrently, that an over-limit acquirer bails, and that the
// count returns to zero after every holder terminals. These tests drive
// real contending node-runs against testcontainers Postgres and assert the
// observable holder rows / RunNode outcomes.
//
// How acquisition is driven: the harness runs with NoSupervisor=true and
// the scheduler enqueues the dispatch rows. The test then drives
// `runtime.RunNode` by hand, one call per contender, exactly as the
// existing claim_scope_conflict_race_test.go does. RunNode runs the full
// §7.3 cycle synchronously (acquire → dispatch → terminal → release), so to
// observe a held-mid-flight lock the test wires a *barrier executor* that
// blocks inside Execute until the test releases it. While a holder is
// parked in the executor, its named-lock claim_handle row is committed and
// `state='active'` (count incremented); the lock decrements only when the
// holder is released and RunNode drives the terminal.
//
// Coupling to limit enforcement (why these FAIL if the limit stops being
// enforced):
//
//   - acquireNamedLock (runtime/runner_acquire_named_locks.go) is the ONLY
//     enforcement site: under the per-name advisory lock it reads
//     CountByNamedLock and returns bail when count >= cfg.Limit.
//     SelectCandidates does NOT pre-filter on named-lock saturation
//     (verified against the postgres queue SQL), so an over-limit contender
//     IS selected, claims its dispatch row, then bails at acquireNamedLock
//     and the per-candidate tx rolls back → RunNode returns Ran=false.
//   - If the `count >= cfg.Limit` check were removed, the over-limit
//     contender would acquire (Ran=true) while a holder is still parked in
//     the barrier executor, and the concurrent-holder assertions below
//     (exactly 1 active row for the mutex; exactly N for the semaphore)
//     would observe limit+1 active rows and FAIL.
//   - If the decrement-at-terminal release were removed (the named-lock
//     Delete in runner_terminal_release.go), the post-terminal "count back
//     to 0" assertion would observe a lingering active row and FAIL.

package locks

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// barrierExecutor is a gRPC Executor that blocks inside Execute until the
// test releases the matching dispatch. It lets a test hold a node-run
// mid-flight — named lock acquired, terminal not yet fired — so the
// concurrent-holder state is directly observable.
//
// Each Execute call signals `entered` (so the test knows the holder has
// reached the executor and therefore committed its acquisition tx), then
// blocks on `release`. The barrier is shared across all dispatches; the
// test sends one token per holder it wants to free.
type barrierExecutor struct {
	genv1.UnimplementedExecutorServer
	entered chan struct{}
	release chan struct{}
}

func newBarrierExecutor() *barrierExecutor {
	return &barrierExecutor{
		entered: make(chan struct{}, 16),
		release: make(chan struct{}, 16),
	}
}

func (b *barrierExecutor) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	// @deliberate: Heartbeat first so the supervisor's stream-read loop has a live
	// frame; then announce arrival and block until the test releases us.
	if err := stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Heartbeat{Heartbeat: &genv1.Heartbeat{
		TimestampMs: time.Now().UnixMilli(),
		Note:        "barrier: entered",
	}}}); err != nil {
		return err
	}
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	delta, err := structpb.NewStruct(map[string]any{"done": true})
	if err != nil {
		return err
	}
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: delta, Changed: true, ChangeSummary: "barrier",
		}}},
	}})
}

// waitEntered blocks until one dispatch has reached the barrier executor.
func (b *barrierExecutor) waitEntered(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(timeout):
		t.Fatalf("barrier executor: no dispatch entered within %s", timeout)
	}
}

// freeOne releases exactly one blocked dispatch.
func (b *barrierExecutor) freeOne() { b.release <- struct{}{} }

// startBarrierExecutor listens on a fresh loopback port and registers the
// barrier executor. Returns the listening address; the server is stopped
// via t.Cleanup.
func startBarrierExecutor(t *testing.T, b *barrierExecutor) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, b)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// activeNamedLockCount returns the number of state='active' named-lock
// holder rows for lockName — the exact predicate CountByNamedLock uses,
// read here directly against the persisted ledger.
func activeNamedLockCount(t *testing.T, h *scenario.Harness, lockName string) int {
	t.Helper()
	var n int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_claim_handles
		  WHERE lock_kind = 'named' AND lock_name = $1 AND state = 'active'`,
		[]any{lockName}, &n)
	return n
}

// @deliberate: runNodeAsync starts one RunNode cycle on its own goroutine under the
// given supervisor id and returns a channel carrying its result. Used to
// launch a holder that will block in the barrier executor.
type runResult struct {
	out runtime.RunnerResult
	err error
}

func makeNamedLockRunArgs(h *scenario.Harness, supID, execAddr string, namedLocks locks.NamedLocksConfig, pool *executor.ClientPool) runtime.RunArgs {
	return runtime.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		ClaimHandles:      h.Persist.ClaimHandles(),
		AdvisoryLocker:    h.Driver.AdvisoryLocker(),
		StoreRegistry:     locks.NewRegistry(),
		NamedLocks:        namedLocks,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      supID,
		AcceptedExecutors: []string{"barrier"},
		AcceptedStores:    []string{},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"barrier": {Transport: "grpc", URL: execAddr},
		}),
		HeartbeatInterval: 1 * time.Second,
	}
}

// TestNamedLockMutexEnforcesMutualExclusion drives two node-runs (two
// instances of a single-node template carrying a `locks:` block for a
// limit:1 named lock) contending on the same mutex. While holder-1 is
// parked in the barrier executor (lock held, count=1), holder-2's RunNode
// must BAIL (Ran=false) — mutual exclusion. Only after holder-1 terminals
// and the lock decrements to 0 may holder-2 acquire (Ran=true).
func TestNamedLockMutexEnforcesMutualExclusion(t *testing.T) {
	t.Parallel()

	const lockName = "mutex-lock"
	barrier := newBarrierExecutor()
	execAddr := startBarrierExecutor(t, barrier)

	namedLocks := locks.NamedLocksConfig{Locks: map[string]locks.NamedLockConfig{
		lockName: {Limit: 1},
	}}

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NamedLocks:   namedLocks,
		ExtraExecutors: map[string]executor.Endpoint{
			"barrier": {Transport: "grpc", URL: execAddr},
		},
	})

	// @deliberate: One template, a single node holding the mutex; two instances so each
	// has its own root frame + dispatch row + node row, letting two manual
	// supervisors contend on the same name without frame-engine coupling.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "named-mutex", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "barrier"},
				scenario.WithLocks(scenario.MutexLock(lockName)),
			),
		},
	})
	iidA := h.CreateInstance(tid, "ck-named-mutex-A", map[string]any{})
	iidB := h.CreateInstance(tid, "ck-named-mutex-B", map[string]any{})

	wA := h.FindNode(iidA, "worker")
	wB := h.FindNode(iidB, "worker")
	require.NotNil(t, wA)
	require.NotNil(t, wB)
	require.True(t, h.WaitForDispatch(wA.ID, 15*time.Second), "wA dispatch row never appeared")
	require.True(t, h.WaitForDispatch(wB.ID, 15*time.Second), "wB dispatch row never appeared")

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	require.Equal(t, 0, activeNamedLockCount(t, h, lockName),
		"precondition: no holders before any RunNode")

	// @deliberate: Launch holder-1; it acquires the mutex (count→1) and blocks in the
	// barrier executor.
	holder1 := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, makeNamedLockRunArgs(h, "sup-1", execAddr, namedLocks, pool), nil)
		holder1 <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)

	// @deliberate: Holder-1 is parked mid-flight with the lock acquired: exactly one
	// active named-lock row exists. (If the limit weren't incremented at
	// acquire, this would be 0; the count is the @blessed-invariant 2
	// observable.)
	require.Equal(t, 1, activeNamedLockCount(t, h, lockName),
		"holder-1 must hold exactly one active named-lock row while parked")

	// @constraint: While holder-1 holds the mutex, holder-2's full RunNode cycle must
	// bail: the candidate is selected and the dispatch row claimed, but
	// acquireNamedLock sees count(1) >= limit(1) and bails, rolling back
	// the per-candidate tx. RunNode therefore reports Ran=false and the
	// barrier executor is NEVER reached by holder-2.
	r2 := mustRunNode(t, h, "sup-2", execAddr, namedLocks, pool)
	require.NoError(t, r2.err, "holder-2 RunNode must not error (a saturated mutex is a soft bail)")
	require.False(t, r2.out.Ran,
		"MUTUAL EXCLUSION: holder-2 must bail (Ran=false) while holder-1 holds the limit:1 named lock")

	// @deliberate: The mutex is still held by exactly one (holder-1); holder-2's bail
	// left no row behind.
	require.Equal(t, 1, activeNamedLockCount(t, h, lockName),
		"after holder-2 bails, still exactly one active named-lock row (holder-1)")

	// @constraint: Holder-2's dispatch row must remain pending+unclaimed (the bail rolled
	// back the dispatch claim), so it is re-acquirable once the mutex frees.
	requireDispatchPending(t, h, wB.ID)

	// @constraint: Release holder-1 → it terminals → the named lock decrements to 0.
	barrier.freeOne()
	r1 := <-holder1
	require.NoError(t, r1.err, "holder-1 RunNode error")
	require.True(t, r1.out.Ran, "holder-1 must have run")

	// @deliberate: Decrement-at-terminal (@blessed-invariant 2): after holder-1's
	// terminal the lock is fully released.
	requireNamedLockCountEventually(t, h, lockName, 0, 5*time.Second)

	// @deliberate: Now the mutex is free, holder-2 can acquire and run to completion.
	// It enters the barrier executor (proving it actually acquired this
	// time, not bailed), then we release it.
	holder2 := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, makeNamedLockRunArgs(h, "sup-2", execAddr, namedLocks, pool), nil)
		holder2 <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)
	require.Equal(t, 1, activeNamedLockCount(t, h, lockName),
		"holder-2 now holds the mutex (count back up to 1) once holder-1 released it")
	barrier.freeOne()
	r2b := <-holder2
	require.NoError(t, r2b.err, "holder-2 second RunNode error")
	require.True(t, r2b.out.Ran,
		"holder-2 must acquire and run AFTER holder-1 released the mutex (second-after-first)")

	// @deliberate: Disposition: after both holders terminal, the named lock is fully
	// released — zero active rows remain.
	requireNamedLockCountEventually(t, h, lockName, 0, 5*time.Second)
}

// TestNamedLockSemaphoreSaturatesAtLimit drives N+1 contenders against a
// limit:N named lock (N=2). At most N may hold concurrently; the (N+1)th
// is gated until a holder terminals. Asserts the saturation bail and the
// post-terminal release back to zero.
func TestNamedLockSemaphoreSaturatesAtLimit(t *testing.T) {
	t.Parallel()

	const (
		lockName = "sem-lock"
		limit    = 2
	)
	barrier := newBarrierExecutor()
	execAddr := startBarrierExecutor(t, barrier)

	namedLocks := locks.NamedLocksConfig{Locks: map[string]locks.NamedLockConfig{
		lockName: {Limit: limit},
	}}

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NamedLocks:   namedLocks,
		ExtraExecutors: map[string]executor.Endpoint{
			"barrier": {Transport: "grpc", URL: execAddr},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "named-semaphore", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "barrier"},
				scenario.WithLocks(scenario.CountingLock(lockName)),
			),
		},
	})

	// @deliberate: N+1 instances, one node each. Track the per-instance worker node id.
	const contenders = limit + 1
	workerIDs := make([]shared.UUID, 0, contenders)
	for i := 0; i < contenders; i++ {
		iid := h.CreateInstance(tid, "ck-named-sem-"+string(rune('A'+i)), map[string]any{})
		w := h.FindNode(iid, "worker")
		require.NotNil(t, w)
		require.True(t, h.WaitForDispatch(w.ID, 15*time.Second), "worker dispatch row never appeared")
		workerIDs = append(workerIDs, w.ID)
	}

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	require.Equal(t, 0, activeNamedLockCount(t, h, lockName),
		"precondition: no holders before any RunNode")

	// @deliberate: Launch the first N holders; each acquires a slot (count climbs to N)
	// and blocks in the barrier executor.
	holders := make([]chan runResult, 0, limit)
	for i := 0; i < limit; i++ {
		ch := make(chan runResult, 1)
		go func(id string) {
			out, err := runtime.RunNode(h.Ctx, makeNamedLockRunArgs(h, id, execAddr, namedLocks, pool), nil)
			ch <- runResult{out: out, err: err}
		}("sup-hold-" + string(rune('1'+i)))
		barrier.waitEntered(t, 15*time.Second)
		holders = append(holders, ch)
		require.Equal(t, i+1, activeNamedLockCount(t, h, lockName),
			"after %d holders entered, exactly %d active semaphore rows", i+1, i+1)
	}

	// @constraint: Semaphore saturated at N. The (N+1)th contender's RunNode must bail:
	// count(N) >= limit(N) → acquireNamedLock bails → Ran=false. The
	// barrier executor is NEVER reached by the extra contender.
	extra := mustRunNode(t, h, "sup-extra", execAddr, namedLocks, pool)
	require.NoError(t, extra.err, "over-limit contender RunNode must not error (saturation is a soft bail)")
	require.False(t, extra.out.Ran,
		"SATURATION: the (N+1)th contender must bail (Ran=false) while N holders saturate the limit:%d named lock", limit)
	require.Equal(t, limit, activeNamedLockCount(t, h, lockName),
		"after the over-limit bail, still exactly N active semaphore rows")

	// @constraint: Release ONE holder → count drops to N-1, opening a single slot.
	barrier.freeOne()
	r := <-holders[0]
	require.NoError(t, r.err, "released holder RunNode error")
	require.True(t, r.out.Ran, "released holder must have run")
	requireNamedLockCountEventually(t, h, lockName, limit-1, 5*time.Second)

	// @deliberate: Now the formerly-gated contender can acquire the freed slot. It
	// enters the barrier executor (proving genuine acquisition), bringing
	// the count back to N.
	gated := make(chan runResult, 1)
	go func() {
		out, err := runtime.RunNode(h.Ctx, makeNamedLockRunArgs(h, "sup-extra", execAddr, namedLocks, pool), nil)
		gated <- runResult{out: out, err: err}
	}()
	barrier.waitEntered(t, 15*time.Second)
	require.Equal(t, limit, activeNamedLockCount(t, h, lockName),
		"the gated contender acquired the freed slot; count back to N")

	barrier.freeOne() // @deliberate: the remaining original holder
	barrier.freeOne() // @deliberate: the late acquirer
	rRemain := <-holders[1]
	require.NoError(t, rRemain.err)
	require.True(t, rRemain.out.Ran)
	rGated := <-gated
	require.NoError(t, rGated.err)
	require.True(t, rGated.out.Ran)

	// @deliberate: Disposition: every holder terminaled → the semaphore is fully
	// released back to zero (@blessed-invariant 2 decrement-at-terminal).
	requireNamedLockCountEventually(t, h, lockName, 0, 5*time.Second)

	_ = workerIDs
}

// mustRunNode runs one RunNode cycle synchronously and returns the result.
func mustRunNode(t *testing.T, h *scenario.Harness, supID, execAddr string, namedLocks locks.NamedLocksConfig, pool *executor.ClientPool) runResult {
	t.Helper()
	out, err := runtime.RunNode(h.Ctx, makeNamedLockRunArgs(h, supID, execAddr, namedLocks, pool), nil)
	return runResult{out: out, err: err}
}

// requireNamedLockCountEventually polls the active named-lock count until
// it matches want or the timeout elapses. The terminal-release tx commits
// slightly after RunNode returns its result on the read connection, so a
// short poll removes that sampling race.
func requireNamedLockCountEventually(t *testing.T, h *scenario.Harness, lockName string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got int
	for time.Now().Before(deadline) {
		got = activeNamedLockCount(t, h, lockName)
		if got == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, want, got,
		"named lock %q: active holder count did not settle to %d within %s", lockName, want, timeout)
}

// requireDispatchPending asserts the node's in-flight dispatch row is back
// to pending + unclaimed — the state a bailed acquisition leaves behind so
// the row is re-acquirable.
func requireDispatchPending(t *testing.T, h *scenario.Harness, nodeID shared.UUID) {
	t.Helper()
	var (
		phase     string
		claimedBy *string
	)
	h.QueryRowSQL(
		`SELECT phase, claimed_by FROM rimsky_node_runs
		  WHERE node_id = $1 AND phase IN ('pending','active','held','parked')`,
		[]any{nodeID}, &phase, &claimedBy)
	require.Equal(t, "pending", phase, "bailed contender's dispatch row must stay pending")
	require.Nil(t, claimedBy, "bailed contender's dispatch claim must be rolled back (claimed_by NULL)")
}
