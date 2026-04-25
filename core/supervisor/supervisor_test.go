// Tests for the supervisor main loop (Plan A Task 10.4). Each test spins up
// a real Postgres via pgtest, a stub gRPC executor, wires a supervisor, and
// drives an end-to-end claim → RunNode → Commit cycle. Uses SystemClock so
// Postgres NOW() governs claim eligibility.
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
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/supervisor"
	"github.com/fallguy/rimsky/executors/stub"
)

// waitFor polls fn up to timeout, returning true if fn returns true before
// the deadline. Keeps tests resilient to the supervisor's polling cadence.
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

// startSupervisorFor stands up a supervisor wired against the fixture, with
// short heartbeat/claim intervals so tests progress quickly. It uses
// SystemClock — the real wall clock drives Postgres NOW() semantics.
func startSupervisorFor(
	t *testing.T,
	f *fixture,
	supervisorID string,
	resolver executor.Resolver,
	getResource func(ctx context.Context, rid shared.UUID) (resource.Resource, error),
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
		GetResource:       getResource,
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

// TestSupervisor_StartsAndRegisters verifies that Start inserts a row into
// rimsky_supervisors with the configured callback host/port + accepted
// executors, and that Shutdown removes it.
func TestSupervisor_StartsAndRegisters(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{"ok": true}, true, "initial")
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
		GetResource:       resolver(map[shared.UUID]resource.Resource{}),
		CallbackHost:      "127.0.0.1",
		CallbackPort:      0,
	})
	require.NoError(t, err)

	row, err := f.sb.Supervisors().Get(ctx, supervisorID, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, []string{"worker"}, row.AcceptedExecutors)
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

// TestSupervisor_ClaimsAndExecutes_Happy verifies the full loop: an
// enqueued stale node is claimed, dispatched to the stub executor, and
// committed (transitions to fresh).
func TestSupervisor_ClaimsAndExecutes_Happy(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	rid, res := f.buildInlineResource(n.ID, nil)

	// Enqueue using real time so enqueued_at <= NOW() is satisfied immediately.
	require.NoError(t, f.q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       n.ID,
		ExecutorName: "worker",
		EnqueuedAt:   time.Now(),
	}))

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{"rows": []any{"a"}}, true, "ok")
	_, addr := s.Listen(t)

	_ = startSupervisorFor(t, f,
		"sup-happy-"+uuid.NewString()[:8],
		resolverFor("worker", addr),
		resolver(map[shared.UUID]resource.Resource{rid: res}),
	)

	// Poll for both conditions together: the node reaches fresh AND the
	// dispatch row is cleared. UpdateState(fresh) runs inside Commit while
	// Queue.Complete runs later in RunNode's outer goroutine, so observing
	// state alone can race ahead of the dispatch-row cleanup.
	require.True(t, waitFor(10*time.Second, func() bool {
		got, _ := f.sb.Nodes().Get(ctx, n.ID, nil)
		if got == nil || got.State != shared.NodeStateFresh {
			return false
		}
		return f.pendingDispatchForNode(n.ID) == nil
	}), "expected node to reach fresh state with dispatch row cleared")

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "commit"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)
}

// TestSupervisor_HeartbeatTick_UpdatesLastHeartbeat verifies the heartbeat
// loop updates rimsky_supervisors.last_heartbeat_at on each tick.
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
		resolver(map[shared.UUID]resource.Resource{}),
	)

	// Wait for initial register, then read the heartbeat, sleep through at
	// least two heartbeat ticks, and assert it advanced.
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

// TestSupervisor_GracefulShutdown_WaitsForActiveRuns enqueues a node whose
// stub delays its terminal event; Shutdown should wait for the run to
// finish (node reaches fresh) before returning.
func TestSupervisor_GracefulShutdown_WaitsForActiveRuns(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	rid, res := f.buildInlineResource(n.ID, nil)

	require.NoError(t, f.q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       n.ID,
		ExecutorName: "worker",
		EnqueuedAt:   time.Now(),
	}))

	// Stub delays each event by 300ms so the terminal lands mid-shutdown.
	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{"ok": true}, true, "slow").Delay(300 * time.Millisecond)
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
		GetResource:       resolver(map[shared.UUID]resource.Resource{rid: res}),
		CallbackHost:      "127.0.0.1",
		CallbackPort:      0,
	})
	require.NoError(t, err)

	// Wait for the supervisor to pick up the dispatch (node transitions to
	// running) so Shutdown actually has an active run to wait on.
	require.True(t, waitFor(5*time.Second, func() bool {
		got, _ := f.sb.Nodes().Get(ctx, n.ID, nil)
		return got != nil && got.State == shared.NodeStateRunning
	}), "expected node to reach running state before shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(shutdownCtx))

	// After Shutdown returns, the in-flight run should have completed.
	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State,
		"expected shutdown to wait for run to commit")

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "commit"), "kinds=%v", kinds)
}
