// Tests for RunNode (Plan A Task 10.3). Spins up a real Postgres via
// pgtest + a real gRPC stub executor (executors/stub), wires in a
// StaticResolver, and drives RunNode end-to-end.
package supervisor_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/supervisor"
	"github.com/fallguy/rimsky/executors/stub"
)

// enqueueAndClaim seeds a dispatch row for nodeID, claims it as
// supervisorID, and returns the claimed row. Shared helper — also used by
// callback_test.go, which notes this is its canonical home.
func (f *fixture) enqueueAndClaim(nodeID shared.UUID, executorName, supervisorID string) shared.DispatchRow {
	f.t.Helper()
	ctx := context.Background()
	require.NoError(f.t, f.q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       nodeID,
		ExecutorName: executorName,
		EnqueuedAt:   f.clock.Now(),
	}))
	row, err := f.q.Claim(ctx, supervisorID, []string{executorName}, nil)
	require.NoError(f.t, err)
	require.NotNil(f.t, row, "expected Claim to return a dispatch row")
	return *row
}

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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRunNode_ClaimLostBeforeRun_ReturnsRanFalse(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	// Someone-else claimed the dispatch row.
	dr := f.enqueueAndClaim(n.ID, "worker", "supervisor-other")

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{"ok": true}, true, "done")
	_, addr := s.Listen(t)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	res, err := supervisor.RunNode(ctx, supervisor.RunArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, DispatchID: dr.ID, SupervisorID: "supervisor-self",
		Pool: pool, Resolver: resolverFor("worker", addr),
		GetResource: resolver(map[shared.UUID]resource.Resource{}),
	}, nil)
	require.NoError(t, err)
	require.False(t, res.Ran)

	// Node still stale (no transition to running).
	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "orphaned_claim_lost_race"), "kinds=%v", kinds)
	require.False(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
}

func TestRunNode_UnresolvedExecutor_EmitsEventAndRoutesOnError(t *testing.T) {
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
	dr := f.enqueueAndClaim(n.ID, "missing-executor", "sup-self")

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	res, err := supervisor.RunNode(ctx, supervisor.RunArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, DispatchID: dr.ID, SupervisorID: "sup-self",
		Pool: pool, Resolver: emptyResolver(),
		GetResource: resolver(map[shared.UUID]resource.Resource{}),
	}, nil)
	require.NoError(t, err)
	require.True(t, res.Ran)

	// OnError with give_up → failed.
	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got.State)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "unresolved_executor"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
	require.False(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
}

func TestRunNode_StubCompletes_CommitsAndTransitionsFresh(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	rid, res := f.buildInlineResource(n.ID, nil)
	dr := f.enqueueAndClaim(n.ID, "worker", "sup-self")

	s := stub.New()
	s.WhenType("worker").Complete(map[string]any{"rows": []any{"a", "b"}}, true, "initial")
	_, addr := s.Listen(t)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	out, err := supervisor.RunNode(ctx, supervisor.RunArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, DispatchID: dr.ID, SupervisorID: "sup-self",
		Pool: pool, Resolver: resolverFor("worker", addr),
		GetResource: resolver(map[shared.UUID]resource.Resource{rid: res}),
	}, nil)
	require.NoError(t, err)
	require.True(t, out.Ran)
	require.False(t, out.Async)

	// Node fresh; work_started + work_completed + commit events.
	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "commit"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)
}

func TestRunNode_StubErrored_RoutesOnErrorClass(t *testing.T) {
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
	dr := f.enqueueAndClaim(n.ID, "worker", "sup-self")

	s := stub.New()
	s.WhenType("worker").Error("BAD_INPUT", map[string]any{"hint": "bad"})
	_, addr := s.Listen(t)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	out, err := supervisor.RunNode(ctx, supervisor.RunArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, DispatchID: dr.ID, SupervisorID: "sup-self",
		Pool: pool, Resolver: resolverFor("worker", addr),
		GetResource: resolver(map[shared.UUID]resource.Resource{}),
	}, nil)
	require.NoError(t, err)
	require.True(t, out.Ran)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	// give_up → failed.
	require.Equal(t, shared.NodeStateFailed, got.State)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

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
	dr := f.enqueueAndClaim(n.ID, "worker", "sup-self")

	s := stub.New()
	s.WhenType("worker").Blocked("awaiting-input", map[string]any{"key": "val"})
	_, addr := s.Listen(t)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	out, err := supervisor.RunNode(ctx, supervisor.RunArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, DispatchID: dr.ID, SupervisorID: "sup-self",
		Pool: pool, Resolver: resolverFor("worker", addr),
		GetResource: resolver(map[shared.UUID]resource.Resource{}),
	}, nil)
	require.NoError(t, err)
	require.True(t, out.Ran)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	// executor_blocked → give_up → failed.
	require.Equal(t, shared.NodeStateFailed, got.State)
	require.Equal(t, "executor_blocked", got.CurrentErrorClass)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

func TestRunNode_StubAsyncAccepted_ReturnsAsyncResult(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	dr := f.enqueueAndClaim(n.ID, "worker", "sup-self")

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

	out, err := supervisor.RunNode(ctx, supervisor.RunArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, DispatchID: dr.ID, SupervisorID: "sup-self",
		Pool: pool, Resolver: resolverFor("worker", addr),
		GetResource: resolver(map[shared.UUID]resource.Resource{}),
		CallbackURL: "http://127.0.0.1:9999/cb",
	}, register)
	require.NoError(t, err)
	require.True(t, out.Ran)
	require.True(t, out.Async)
	require.Equal(t, "ack-xyz", out.AsyncAckID)
	require.Equal(t, "ack-xyz", gotAckID)
	require.Equal(t, n.ID, gotCtx.NodeID)
	require.Equal(t, dr.ID, gotCtx.DispatchID)
	require.Equal(t, "sup-self", gotCtx.SupervisorID)

	// Node still running — terminal outcome has not arrived yet.
	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateRunning, got.State)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
}

func TestRunNode_DialFailure_InfraError(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	ctx := context.Background()

	n := f.addStaleNode("worker", "worker")
	dr := f.enqueueAndClaim(n.ID, "worker", "sup-self")

	// Pick a closed port: listen + close to get a deterministically-free port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	bogus := l.Addr().String()
	require.NoError(t, l.Close())

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// Wrap context with short timeout so we don't wait on grpc's default
	// backoff/reconnect forever if the executor happens to come up.
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := supervisor.RunNode(runCtx, supervisor.RunArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, DispatchID: dr.ID, SupervisorID: "sup-self",
		Pool: pool, Resolver: resolverFor("worker", bogus),
		GetResource: resolver(map[shared.UUID]resource.Resource{}),
	}, nil)
	require.NoError(t, err)
	require.True(t, out.Ran)

	// Infra-error path: running→stale via heartbeat_lost and a fresh dispatch
	// row was re-enqueued.
	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	require.NotNil(t, f.pendingDispatchForNode(n.ID), "expected re-enqueue after infra error")

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "work_started"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

// guard: avoid `fmt` being removed by goimports if tests shrink.
var _ = fmt.Sprintf

// guard: ensure uuid import stays live even if tests shrink.
var _ = uuid.New

// guard: keep storage alias used if we later inspect rows.
var _ = storage.EventListFilter{}
