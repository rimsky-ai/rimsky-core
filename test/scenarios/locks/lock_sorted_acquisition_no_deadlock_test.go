// §19.1 — sorted multi-lock acquisition: a node requiring two named
// locks acquired by multiple supervisors in parallel must not deadlock
// under contention.
//
// Verifies blessed invariant 3 (spec §18 invariant 3, §13.7): "Multi-
// lock acquisition uses deterministic sorted order." Without the sort
// order on `(lock_kind, sort_key)` two supervisors holding different
// orderings of the same advisory-lock set deadlock; with it the second
// supervisor blocks on the first's advisory lock and only proceeds
// once the first commits or rolls back.
//
// Mechanism: define a single template node that requires TWO named
// locks ("lock-alpha" and "lock-beta"), both mutex. Run RunNode from
// N goroutines concurrently with distinct SupervisorIDs against the
// same node. Each call independently runs the §13.3 acquisition tx;
// they serialize on the per-lock advisory locks taken in §13.7 sort
// order. No goroutine may stall indefinitely; the test enforces this
// with a hard deadline.
//
// Run under `-race -count=10` per the task verification steps.
//
// We expect each goroutine to either claim the dispatch row exactly
// once (Ran=true) or bail (Ran=false). On total runs = N, exactly one
// claim succeeds (the first to commit); the others bail because the
// dispatch row is already claimed by the time they try.
//
// The synchronous stub Complete commits the run and releases the lock
// holder rows so a follow-up RunNode (post-completion) would also
// succeed; we deliberately keep N small enough that the timing is
// observable on CI.
package locks

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/supervisor"
)

func TestLockSortedAcquisitionNoDeadlock(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	h.Stub.WhenType("multilock").Complete(map[string]any{}, true, "ok")

	// Two named mutex locks. The runner sorts these by sort_key
	// ("lock-alpha" < "lock-beta") inside §13.7 so every supervisor
	// takes them in the same advisory-lock order.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "multilock", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "multilock", Executor: "stub"},
				scenario.WithLocks(
					scenario.MutexLock("lock-alpha"),
					scenario.MutexLock("lock-beta"),
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-multilock", map[string]any{})

	worker := h.FindNode(iid, "multilock")
	require.NotNil(t, worker)

	// Force the dispatch row eligible.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_dispatch
		    SET executor_name = 'stub',
		        required_stores = '{}',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        enqueued_at = NOW() - INTERVAL '5 seconds'
		  WHERE node_id = $1`,
		worker.ID,
	)
	require.NoError(t, err)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	resolver := executor.NewStaticResolver(map[string]executor.Endpoint{
		"stub": {Transport: "grpc", URL: h.StubAddr},
	})

	const numSupervisors = 5

	// Run N supervisors concurrently. Each gets a distinct
	// SupervisorID so the advisory-lock contention is real (the
	// per-tx advisory lock is keyed on lock name; the dispatch row's
	// claimant-guarded UPDATE filters on supervisor_id).
	var wg sync.WaitGroup
	var ranCount atomic.Int32
	results := make(chan struct {
		ran bool
		err error
	}, numSupervisors)

	deadline := time.Now().Add(15 * time.Second)
	for i := 0; i < numSupervisors; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			args := supervisor.RunArgs{
				Storage:           h.Storage,
				Queue:             h.Queue,
				QueuePool:         h.Pool,
				LockHolders:       store.NewLockHoldersClient(h.Pool),
				StoreRegistry:     h.Stores,
				Clock:             shared.SystemClock{},
				Logger:            shared.SilentLogger{},
				SupervisorID:      supIDfor(id),
				AcceptedExecutors: []string{"stub"},
				Pool:              pool,
				Resolver:          resolver,
				HeartbeatInterval: 100 * time.Millisecond,
			}
			out, err := supervisor.RunNode(h.Ctx, args, nil)
			if out.Ran {
				ranCount.Add(1)
			}
			results <- struct {
				ran bool
				err error
			}{ran: out.Ran, err: err}
		}(i)
	}

	// Wait, but enforce a hard deadline. The §13.7 invariant guarantees
	// no deadlock: every goroutine completes within the inner stub
	// roundtrip + advisory-lock serialisation time. 15s is generous;
	// any actual deadlock surfaces as a timeout.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Until(deadline)):
		t.Fatal("RunNode goroutines did not complete within deadline; possible deadlock")
	}
	close(results)

	// Surface any errors from the goroutines.
	for r := range results {
		require.NoError(t, r.err, "RunNode in concurrent goroutine returned error")
	}

	// Exactly one supervisor claims the dispatch row (the others see
	// it already claimed). The synchronous stub Complete drives the
	// claimant to fresh.
	require.Equal(t, int32(1), ranCount.Load(),
		"exactly one supervisor must succeed; got %d", ranCount.Load())

	got, err := h.Storage.Nodes().Get(h.Ctx, worker.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State,
		"worker should reach fresh after the synchronous stub Complete")

	// Both lock-holder rows are gone post-commit (§13.6 release tx).
	holders, err := h.Storage.LockHolders().ListByHolderNode(h.Ctx, worker.ID, nil)
	require.NoError(t, err)
	require.Empty(t, holders, "both named-lock holder rows should be released after commit")
}

// supIDfor returns a stable SupervisorID per goroutine ordinal. Plain
// string concat keeps the helper trivial; it lives next to the test
// because no other test references it.
func supIDfor(i int) string {
	return "sup-" + string(rune('A'+i))
}
