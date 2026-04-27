// Tests for the omnibus runner (`supervisor.RunNode`) under the
// stores-redesign shape. Each test boots a fresh Postgres via pgtest, a
// real gRPC stub executor (executors/stub), wires a `core/store` registry
// of stub stores, and drives `RunNode` end-to-end. The runner does its
// own §13.3 candidate selection inside the call, so tests enqueue a
// dispatch row and rely on RunNode to pick it up — there is no separate
// pre-claim helper anymore.
package supervisor_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/supervisor"
	"github.com/fallguy/rimsky/executors/stub"
)

// resolverFor returns an executor.StaticResolver with a single mapping.
func resolverFor(execName, addr string) *executor.StaticResolver {
	return executor.NewStaticResolver(map[string]executor.Endpoint{
		execName: {Transport: "grpc", URL: addr},
	})
}

// emptyResolver has no executors configured — every Resolve is a miss.
func emptyResolver() *executor.StaticResolver {
	return executor.NewStaticResolver(map[string]executor.Endpoint{})
}

// newRunArgs builds a RunArgs bundle suitable for the omnibus runner. Tests
// override individual fields (Resolver, AcceptedExecutors, etc.) before
// calling RunNode. The pool / lock-holders client / store registry come
// from the fixture.
func (f *fixture) newRunArgs(supervisorID string, pool *executor.ClientPool, resolver executor.Resolver) supervisor.RunArgs {
	return supervisor.RunArgs{
		Storage:           f.sb,
		Queue:             f.q,
		QueuePool:         f.pool,
		LockHolders:       f.lockHolders,
		StoreRegistry:     f.registry,
		Clock:             f.clock,
		Logger:            f.log,
		SupervisorID:      supervisorID,
		AcceptedExecutors: []string{"worker"},
		Pool:              pool,
		Resolver:          resolver,
		HeartbeatInterval: 100 * time.Millisecond,
	}
}

// enqueue inserts a dispatch row pointing at the supplied node, enqueued
// for immediate eligibility. Sources frame_id from the node row (seeded
// by addStaleNode/addRunningNode via ensureRunningFrame) to satisfy
// blessed-invariant 19.
func (f *fixture) enqueue(nodeID shared.UUID, executorName string) {
	f.t.Helper()
	ctx := context.Background()
	n, err := f.sb.Nodes().Get(ctx, nodeID, nil)
	require.NoError(f.t, err)
	require.NotNil(f.t, n.FrameID, "enqueue requires node frame_id; ensure addStaleNode/addRunningNode was used")
	require.NoError(f.t, f.q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       nodeID,
		ExecutorName: executorName,
		EnqueuedAt:   f.clock.Now(),
		FrameID:      *n.FrameID,
	}))
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRunNode_NoCandidates_ReturnsRanFalse exercises the §13.3 step 1
// short-tx that returns no candidates: with an empty queue the runner
// should bail quickly with Ran=false and no side effects.
func TestRunNode_NoCandidates_ReturnsRanFalse(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	out, err := supervisor.RunNode(ctx, f.newRunArgs("sup-self", pool, emptyResolver()), nil)
	require.NoError(t, err)
	require.False(t, out.Ran, "expected Ran=false on empty queue")
}

// TestRunNode_StubCompletes_TransitionsFresh exercises the happy path:
// stale node → enqueue → RunNode claims, dispatches to stub, applies
// terminalKindComplete, transitions to fresh. Asserts on lock-holder
// cleanup and event audit trail.
func TestRunNode_StubCompletes_TransitionsFresh(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	f.enqueue(n.ID, "worker")

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{}, true, "initial")
	_, addr := s.Listen(t)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	out, err := supervisor.RunNode(ctx, f.newRunArgs("sup-self", pool, resolverFor("worker", addr)), nil)
	require.NoError(t, err)
	require.True(t, out.Ran)
	require.False(t, out.Async)
	require.Equal(t, n.ID, out.NodeID)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "attributes_committed"), "kinds=%v", kinds)

	// No lock-holder rows configured (template declares no stores), so
	// the release loop should be a no-op walk and leave nothing behind.
	require.False(t, f.hasLockHolderForNode(n.ID))
}

// TestRunNode_UnresolvedExecutor_RoutesGiveUp covers the §17.1 step 4a
// resolver-miss branch: the runner emits unresolved_executor, classifies
// the terminal as Errored{unresolved_executor}, and runs the policy
// chain. With an explicit give_up override the node lands in failed.
func TestRunNode_UnresolvedExecutor_RoutesGiveUp(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{
			Type: "worker", Executor: "missing-executor",
			ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
				"unresolved_executor": {Policy: []nodepkg.PolicyAction{
					{Action: "give_up"},
				}},
			},
		},
	})
	ctx := context.Background()

	n := f.addStaleNode("worker", "missing-executor")
	f.enqueue(n.ID, "missing-executor")

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	args := f.newRunArgs("sup-self", pool, emptyResolver())
	args.AcceptedExecutors = []string{"missing-executor"}

	out, err := supervisor.RunNode(ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got.State)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "unresolved_executor"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

// TestRunNode_StubErrored_RoutesPolicyChain covers the policy-chain
// terminal classification: an Errored event with a known error class
// flows through `applyTerminalAppError` → give_up → failed.
func TestRunNode_StubErrored_RoutesPolicyChain(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{
			Type: "worker", Executor: "worker",
			ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
				"BAD_INPUT": {Policy: []nodepkg.PolicyAction{
					{Action: "give_up"},
				}},
			},
		},
	})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	f.enqueue(n.ID, "worker")

	s := stub.New()
	s.WhenType("worker").Error("BAD_INPUT", map[string]any{"hint": "bad"})
	_, addr := s.Listen(t)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	out, err := supervisor.RunNode(ctx, f.newRunArgs("sup-self", pool, resolverFor("worker", addr)), nil)
	require.NoError(t, err)
	require.True(t, out.Ran)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got.State)
	require.Equal(t, "BAD_INPUT", got.CurrentErrorClass)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

// TestRunNode_StubBlocked_RoutesExecutorBlockedClass exercises the
// Blocked branch of `readExecutorStream` → `applyTerminalAppError` with
// the synthetic `executor_blocked` class.
func TestRunNode_StubBlocked_RoutesExecutorBlockedClass(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{
			Type: "worker", Executor: "worker",
			ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
				"executor_blocked": {Policy: []nodepkg.PolicyAction{
					{Action: "give_up"},
				}},
			},
		},
	})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	f.enqueue(n.ID, "worker")

	s := stub.New()
	s.WhenType("worker").Blocked("awaiting-input", map[string]any{"key": "val"})
	_, addr := s.Listen(t)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	out, err := supervisor.RunNode(ctx, f.newRunArgs("sup-self", pool, resolverFor("worker", addr)), nil)
	require.NoError(t, err)
	require.True(t, out.Ran)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got.State)
	require.Equal(t, "executor_blocked", got.CurrentErrorClass)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

// TestRunNode_StubAsyncAccepted_RegistersAsyncContext exercises the
// AsyncAccepted handoff: the runner returns Async=true with the ackID,
// the registerAsync callback fires with an enriched AsyncContext, and
// the node stays in `running` until the callback POST arrives. The
// AsyncContext now carries NodeType / Executor / NodeDef /
// ResolvedAttributes / AttributesSchema / StoreRegistry per the
// redesign — assert on each.
func TestRunNode_StubAsyncAccepted_RegistersAsyncContext(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	f.enqueue(n.ID, "worker")

	s := stub.New()
	s.WhenType("worker").AsyncAccepted("ack-xyz", 60000)
	_, addr := s.Listen(t)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	var gotAckID string
	var gotCtx supervisor.AsyncContext
	register := func(ackID string, actx supervisor.AsyncContext) {
		gotAckID = ackID
		gotCtx = actx
	}

	args := f.newRunArgs("sup-self", pool, resolverFor("worker", addr))
	args.CallbackURL = "http://127.0.0.1:9999/cb"

	out, err := supervisor.RunNode(ctx, args, register)
	require.NoError(t, err)
	require.True(t, out.Ran)
	require.True(t, out.Async)
	require.Equal(t, "ack-xyz", out.AsyncAckID)
	require.Equal(t, "ack-xyz", gotAckID)

	require.Equal(t, n.ID, gotCtx.NodeID)
	require.Equal(t, "sup-self", gotCtx.SupervisorID)
	require.Equal(t, "worker", gotCtx.NodeType)
	require.Equal(t, "worker", gotCtx.Executor)
	require.NotNil(t, gotCtx.NodeDef, "AsyncContext should carry NodeDef for the policy chain")
	require.Equal(t, f.registry, gotCtx.StoreRegistry, "AsyncContext should reference the supervisor's StoreRegistry")

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateRunning, got.State, "expected node to stay running until callback")

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
}

// TestRunNode_DialFailure_InfraReenqueue covers the §17.1 step 4a infra
// branch: when the executor dial fails, the runner classifies the
// terminal as terminalKindInfra → applyTerminalInfraError → re-enqueue
// without retry-counter bump. The node returns to stale and a fresh
// dispatch row sits with enqueued_at = now().
func TestRunNode_DialFailure_InfraReenqueue(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	f.enqueue(n.ID, "worker")

	// Pick a closed port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	bogus := l.Addr().String()
	require.NoError(t, l.Close())

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := supervisor.RunNode(runCtx, f.newRunArgs("sup-self", pool, resolverFor("worker", bogus)), nil)
	require.NoError(t, err)
	require.True(t, out.Ran)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	require.NotNil(t, f.pendingDispatchForNode(n.ID), "expected re-enqueue after infra error")

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

// guard: keep storage alias used if we later inspect rows.
var _ = storage.EventListFilter{}

// guard: ensure uuid import stays live even if tests shrink.
var _ = uuid.New
