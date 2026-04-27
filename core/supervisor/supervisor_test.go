// End-to-end tests for the supervisor process loop. Each test boots a
// real Postgres via pgtest, a stub gRPC executor, wires a `core/store`
// registry of stub stores, and runs the full claim → RunNode → release
// cycle through `supervisor.Start`. Uses SystemClock so Postgres NOW()
// governs eligibility windows.
//
// The supervisor's `Config.StoreRegistry` is the load-bearing wiring —
// without it Start returns an error per spec §14.2 (the `accepted_stores`
// set is derived from the registry's store names).
package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/supervisor"
	"github.com/fallguy/rimsky/executors/stub"
)

// waitFor polls fn up to timeout, returning true if fn returns true before
// the deadline.
func waitFor(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// startSupervisorFor stands up a supervisor wired against the fixture,
// with short heartbeat/claim intervals for fast tests. Uses SystemClock —
// the real wall clock governs Postgres NOW() semantics.
func startSupervisorFor(
	t *testing.T,
	f *fixture,
	supervisorID string,
	resolver executor.Resolver,
) *supervisor.Handle {
	t.Helper()
	h, err := supervisor.Start(supervisor.Config{
		SupervisorID:      supervisorID,
		Storage:           f.sb,
		Queue:             f.q,
		Clock:             shared.SystemClock{},
		Logger:            f.log,
		Concurrency:       2,
		HeartbeatInterval: 100 * time.Millisecond,
		ClaimPollInterval: 50 * time.Millisecond,
		Resolver:          resolver,
		StoreRegistry:     f.registry,
		CallbackHost:      "127.0.0.1",
		CallbackPort:      0,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	})
	return h
}

// TestSupervisor_StartsAndRegisters verifies that Start inserts a row
// into rimsky_supervisors with the configured callback host/port,
// `accepted_executors` derived from the resolver, and `accepted_stores`
// derived from the StoreRegistry — and that Shutdown removes it.
func TestSupervisor_StartsAndRegisters(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{}, false, "noop")
	_, addr := s.Listen(t)

	supervisorID := "sup-register-" + uuid.NewString()[:8]
	h, err := supervisor.Start(supervisor.Config{
		SupervisorID:      supervisorID,
		Storage:           f.sb,
		Queue:             f.q,
		Clock:             shared.SystemClock{},
		Logger:            f.log,
		Concurrency:       2,
		HeartbeatInterval: 100 * time.Millisecond,
		ClaimPollInterval: 50 * time.Millisecond,
		Resolver:          resolverFor("worker", addr),
		StoreRegistry:     f.registry,
		CallbackHost:      "127.0.0.1",
		CallbackPort:      0,
	})
	require.NoError(t, err)

	row, err := f.sb.Supervisors().Get(ctx, supervisorID, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, []string{"worker"}, row.AcceptedExecutors)
	require.ElementsMatch(t, []string{"fs", "claims"}, row.AcceptedStores,
		"AcceptedStores should mirror the registry's store-name set")
	require.Equal(t, 2, row.Concurrency)
	require.Equal(t, "127.0.0.1", row.CallbackHost)
	require.Greater(t, row.CallbackPort, 0)
	require.Contains(t, h.CallbackAddr(), "127.0.0.1:")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(shutdownCtx))

	gone, err := f.sb.Supervisors().Get(ctx, supervisorID, nil)
	require.NoError(t, err)
	require.Nil(t, gone, "expected supervisor row to be removed after Shutdown")
}

// TestSupervisor_StartsRequiresStoreRegistry asserts the §14.2 contract:
// supervisor.Start fails fast when StoreRegistry is nil. The supervisor
// cannot register `accepted_stores` and the omnibus runner cannot
// resolve store-side AcquireLock without it.
func TestSupervisor_StartsRequiresStoreRegistry(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{}, false, "noop")
	_, addr := s.Listen(t)

	_, err := supervisor.Start(supervisor.Config{
		SupervisorID:  "sup-no-registry-" + uuid.NewString()[:8],
		Storage:       f.sb,
		Queue:         f.q,
		Clock:         shared.SystemClock{},
		Logger:        f.log,
		Resolver:      resolverFor("worker", addr),
		StoreRegistry: nil,
		CallbackHost:  "127.0.0.1",
		CallbackPort:  0,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "StoreRegistry")
}

// TestSupervisor_ClaimsAndExecutes_Happy verifies the full loop: an
// enqueued stale node is claimed, dispatched to the stub executor, and
// committed (transitions to fresh). The dispatch row is cleaned up by
// the supervisor's RunNode goroutine on a non-async run.
func TestSupervisor_ClaimsAndExecutes_Happy(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	require.NotNil(t, n.FrameID)

	require.NoError(t, f.q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       n.ID,
		ExecutorName: "worker",
		EnqueuedAt:   time.Now(),
		FrameID:      *n.FrameID,
	}))

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{}, true, "ok")
	_, addr := s.Listen(t)

	_ = startSupervisorFor(t, f,
		"sup-happy-"+uuid.NewString()[:8],
		resolverFor("worker", addr),
	)

	require.True(t, waitFor(10*time.Second, func() bool {
		got, _ := f.sb.Nodes().Get(ctx, n.ID, nil)
		if got == nil || got.State != shared.NodeStateFresh {
			return false
		}
		return f.pendingDispatchForNode(n.ID) == nil
	}), "expected node to reach fresh state with dispatch row cleared")

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "attributes_committed"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)
}

// TestSupervisor_HeartbeatTick_UpdatesLastHeartbeat verifies the
// heartbeat loop updates rimsky_supervisors.last_heartbeat_at.
func TestSupervisor_HeartbeatTick_UpdatesLastHeartbeat(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{}, false, "noop")
	_, addr := s.Listen(t)

	supervisorID := "sup-hb-" + uuid.NewString()[:8]
	_ = startSupervisorFor(t, f, supervisorID,
		resolverFor("worker", addr),
	)

	var first time.Time
	require.True(t, waitFor(3*time.Second, func() bool {
		row, _ := f.sb.Supervisors().Get(ctx, supervisorID, nil)
		if row == nil {
			return false
		}
		first = row.LastHeartbeatAt
		return !first.IsZero()
	}), "expected initial heartbeat to be recorded")

	require.True(t, waitFor(3*time.Second, func() bool {
		row, _ := f.sb.Supervisors().Get(ctx, supervisorID, nil)
		return row != nil && row.LastHeartbeatAt.After(first)
	}), "expected heartbeat to advance (first=%v)", first)
}

// TestSupervisor_GracefulShutdown_WaitsForActiveRuns enqueues a node
// whose stub delays its terminal event; Shutdown should wait for the
// run to commit (node reaches fresh) before returning.
func TestSupervisor_GracefulShutdown_WaitsForActiveRuns(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	require.NotNil(t, n.FrameID)

	require.NoError(t, f.q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       n.ID,
		ExecutorName: "worker",
		EnqueuedAt:   time.Now(),
		FrameID:      *n.FrameID,
	}))

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{}, true, "slow").Delay(300 * time.Millisecond)
	_, addr := s.Listen(t)

	h, err := supervisor.Start(supervisor.Config{
		SupervisorID:      "sup-graceful-" + uuid.NewString()[:8],
		Storage:           f.sb,
		Queue:             f.q,
		Clock:             shared.SystemClock{},
		Logger:            f.log,
		Concurrency:       1,
		HeartbeatInterval: 100 * time.Millisecond,
		ClaimPollInterval: 50 * time.Millisecond,
		Resolver:          resolverFor("worker", addr),
		StoreRegistry:     f.registry,
		CallbackHost:      "127.0.0.1",
		CallbackPort:      0,
	})
	require.NoError(t, err)

	require.True(t, waitFor(5*time.Second, func() bool {
		got, _ := f.sb.Nodes().Get(ctx, n.ID, nil)
		return got != nil && got.State == shared.NodeStateRunning
	}), "expected node to reach running state before shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(shutdownCtx))

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State,
		"expected shutdown to wait for run to commit")

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "attributes_committed"), "kinds=%v", kinds)
}
