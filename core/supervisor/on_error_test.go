// Tests for OnError. Uses the shared fixture from commit_test.go.
package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/supervisor"
)

func TestOnError_Retry_ReEnqueuesWithBackoff(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{
			Type: "worker", Executor: "worker",
			ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
				"boom": {Policy: []nodepkg.PolicyAction{
					{Action: "retry", Count: 2, Backoff: shared.BackoffLinear, BaseDelayMs: 100, MaxDelayMs: 100},
				}},
			},
		},
	})
	ctx := context.Background()
	n := f.addRunningNode("worker", "worker")

	err := supervisor.OnError(ctx, supervisor.OnErrorArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, InstanceID: f.instance,
		ErrorClass: "boom", Payload: map[string]any{"x": 1},
	})
	require.NoError(t, err)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)
	require.Equal(t, "boom", got.CurrentErrorClass)
	require.Equal(t, 1, got.RetryCounter)

	// Dispatch row enqueued with future enqueued_at.
	dr := f.pendingDispatchForNode(n.ID)
	require.NotNil(t, dr)
	// Linear backoff, attempt 0 → base_delay_ms.
	require.WithinDuration(t, f.clock.Now().Add(100*time.Millisecond), dr.EnqueuedAt, 20*time.Millisecond)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

func TestOnError_Invalidate_EmitsInvalidateToTargets(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{
			Type: "worker", Executor: "worker",
			ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
				"reset": {Policy: []nodepkg.PolicyAction{
					{Action: "invalidate", Targets: []string{"upstream"}},
				}},
			},
		},
		{Type: "upstream", Executor: "worker"},
	})
	ctx := context.Background()

	// upstream starts fresh so InvalidateNode can transition it to stale.
	// restore_version is valid from any state → fresh.
	upstream := f.addStaleNode("upstream", "worker")
	require.NoError(t, f.sb.Nodes().UpdateState(ctx, upstream.ID, shared.NodeStateFresh, nodepkg.ReasonRestoreVersion, nil))

	worker := f.addRunningNode("worker", "worker")

	err := supervisor.OnError(ctx, supervisor.OnErrorArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: worker.ID, InstanceID: f.instance,
		ErrorClass: "reset", Payload: map[string]any{},
	})
	require.NoError(t, err)

	// Worker transitioned running→stale.
	got, err := f.sb.Nodes().Get(ctx, worker.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	// Upstream was invalidated (fresh → stale).
	upGot, err := f.sb.Nodes().Get(ctx, upstream.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, upGot.State)

	// Error event logged on worker.
	require.True(t, containsString(f.eventKinds(worker.ID), "error"))
	// Upstream has message_received and message_emitted.
	upKinds := f.eventKinds(upstream.ID)
	require.True(t, containsString(upKinds, "message_received"), "upstream kinds=%v", upKinds)
}

func TestOnError_GiveUp_TransitionsFailed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{
			Type: "worker", Executor: "worker",
			ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
				"fatal": {Policy: []nodepkg.PolicyAction{
					{Action: "give_up", ReasonTemplate: "unrecoverable"},
				}},
			},
		},
	})
	ctx := context.Background()
	n := f.addRunningNode("worker", "worker")

	err := supervisor.OnError(ctx, supervisor.OnErrorArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, InstanceID: f.instance,
		ErrorClass: "fatal",
	})
	require.NoError(t, err)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got.State)

	// No pending dispatch.
	require.Nil(t, f.pendingDispatchForNode(n.ID))
	require.True(t, containsString(f.eventKinds(n.ID), "error"))
}

func TestOnError_UnresolvedTargets_LogsEventAndContinues(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{
			Type: "worker", Executor: "worker",
			ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
				"reset": {Policy: []nodepkg.PolicyAction{
					{Action: "invalidate", Targets: []string{"does_not_exist"}},
				}},
			},
		},
	})
	ctx := context.Background()
	n := f.addRunningNode("worker", "worker")

	err := supervisor.OnError(ctx, supervisor.OnErrorArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: n.ID, InstanceID: f.instance,
		ErrorClass: "reset",
	})
	require.NoError(t, err)

	// Worker transitioned to stale.
	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	// unresolved_invalidate_target event logged on the source.
	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "unresolved_invalidate_target"),
		"expected unresolved_invalidate_target, kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"))
}
