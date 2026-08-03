// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func assertWorkPair(t *testing.T, h *scenario.Harness, nodeID foundationshared.UUID, wantTerminalKind string) {
	t.Helper()
	completed := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &nodeID, Kind: "work_completed"})
	started := eventwait.Events(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &nodeID, Kind: "work_started"})

	require.Len(t, started, 1, "expected exactly one work_started for the run")
	require.Len(t, completed, 1, "expected exactly one work_completed for the run")

	s, c := started[0], completed[0]
	require.NotNil(t, s.NodeID)
	require.NotNil(t, c.NodeID)
	require.Equal(t, nodeID, *s.NodeID, "work_started node id")
	require.Equal(t, nodeID, *c.NodeID, "work_completed node id")

	sDispatch, ok := s.Payload["dispatch_id"].(string)
	require.True(t, ok, "work_started payload must carry dispatch_id, got %v", s.Payload)
	require.NotEmpty(t, sDispatch)
	cDispatch, ok := c.Payload["dispatch_id"].(string)
	require.True(t, ok, "work_completed payload must carry dispatch_id, got %v", c.Payload)
	require.Equal(t, sDispatch, cDispatch, "work_started / work_completed must pair on dispatch_id")

	require.Equal(t, s.Payload["supervisor_id"], c.Payload["supervisor_id"],
		"work_started / work_completed must carry the same supervisor_id")
	require.Equal(t, wantTerminalKind, c.Payload["terminal_kind"],
		"work_completed must carry the terminal kind")
	require.False(t, c.OccurredAt.Before(s.OccurredAt),
		"work_completed must not precede its work_started twin")
}

func TestWorkCompletedPairsWorkStartedOnComplete(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "work-completed-pairing", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-work-completed", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	assertWorkPair(t, h, n.ID, "complete")
}

func TestWorkCompletedSingletonAcrossRetryIterations(t *testing.T) {
	t.Parallel()
	const maxRetries = 2
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("retrier").Error("always_err", map[string]any{"hint": "permanent"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "work-completed-retry-singleton", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "retrier", Executor: "stub",
				MaxRetries:   node.IntPtr(maxRetries),
				RetryBackoff: &node.RetryBackoffConfig{BaseDelayMs: 10, Kind: "linear"},
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/always_err": {Action: "retry"},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-work-completed-retry", map[string]any{})

	n := h.FindNode(iid, "retrier")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFailed)

	retries := eventwait.Events(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, KindPrefix: "transient/retry/"})
	require.Len(t, retries, maxRetries,
		"precondition: the run must actually iterate through %d in-place retries", maxRetries)

	assertWorkPair(t, h, n.ID, "errored")
}

// @story: work-completed-emitted
// @decision: prior-stale-recovery-rename
func TestWorkCompletedSingletonAcrossLivenessRecovery(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})
	h.Stub.WhenType("agent").
		AwaitAsyncCallback("ack-liveness-recovery", 600000).
		Then().Success(map[string]any{"ok": true}, true, "recovered")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "work-completed-liveness-recovery", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "agent", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-work-completed-liveness-recovery", map[string]any{})

	n := h.FindNode(iid, "agent")
	require.NotNil(t, n)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })
	args := runtime.RunArgs{
		Persist:               h.Persist,
		Queue:                 h.Queue,
		ClaimHandles:          h.Persist.ClaimHandles(),
		AdvisoryLocker:        h.Driver.AdvisoryLocker(),
		ClaimProducerRegistry: locks.NewRegistry(),
		Clock:                 foundationshared.SystemClock{},
		Logger:                foundationshared.SilentLogger{},
		SupervisorID:          "scenario-runner-liveness-recovery",
		Pool:                  pool,
		MaxQuietPeriodDefault: time.Second,
		CallbackURL:           "http://callback.invalid",
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
	}

	handoff, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, handoff.Ran, "precondition: the first acquisition must dispatch the node")

	started := eventwait.Events(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "work_started"})
	require.Len(t, started, 1, "precondition: the first acquisition announces the episode")

	require.NoError(t, runtime.SweepExecutorDeadlines(h.Ctx, runtime.ConductorArgs{
		Persist: h.Persist,
		Queue:   h.Queue,
		Clock:   foundationshared.NewControllableClock(time.Now().UTC().Add(24 * time.Hour)),
		Logger:  foundationshared.SilentLogger{},
	}))

	reaped := eventwait.Events(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "orphaned_claim_released"})
	require.Len(t, reaped, 1,
		"precondition: the quiet dispatch must be swept back to acquisition-eligible")

	recovered, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, recovered.Ran, "the swept dispatch must be re-acquired and run to terminal")

	assertWorkPair(t, h, n.ID, "complete")
}

func TestWorkCompletedPairsWorkStartedOnErrored(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("flaky").Error("my_err", map[string]any{"hint": "boom"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "work-completed-pairing-errored", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "flaky", Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/my_err": {
						Action: "give_up",
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-work-completed-err", map[string]any{})

	n := h.FindNode(iid, "flaky")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFailed)

	assertWorkPair(t, h, n.ID, "errored")
}

func TestWorkCompletedNotEmittedOnPark(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").
		Park(time.Now().Add(time.Hour))

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "work-completed-park", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-work-completed-park", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateParked)

	started := eventwait.Events(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "work_started"})
	require.Len(t, started, 1,
		"a park must still emit its work_started pairing half; work happened, it just didn't finish")

	completed := eventwait.Events(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "work_completed"})
	require.Empty(t, completed,
		"a park must emit no work_completed event; the run re-enters and its eventual terminal emits the pairing event")
}
