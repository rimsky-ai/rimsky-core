// Tests for CallbackServer. Uses the shared fixture from commit_test.go.
package supervisor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/supervisor"
)

// startCallbackServer builds a CallbackServer against the fixture and starts
// it on an OS-assigned port. The returned cleanup shuts it down.
func startCallbackServer(t *testing.T, f *fixture, reg *supervisor.CallbackRegistry) (string, func()) {
	t.Helper()
	srv := &supervisor.CallbackServer{
		Registry: reg,
		Storage:  f.sb,
		Queue:    f.q,
		Clock:    f.clock,
		Logger:   f.log,
	}
	addr, err := srv.Start("127.0.0.1", 0)
	require.NoError(t, err)
	return addr, func() {
		_ = srv.Close(context.Background())
	}
}

// postCallback sends a JSON body to the /v1/callback/:ackID endpoint and
// returns (status, body).
func postCallback(t *testing.T, addr, ackID string, body any) (int, []byte) {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	url := fmt.Sprintf("http://%s/v1/callback/%s", addr, ackID)
	resp, err := http.Post(url, "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, out
}

// Note: fixture.enqueueAndClaim is defined in runner_test.go.

func TestCallback_UnknownAckID_Returns404(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "probe", Executor: "worker"}})
	reg := supervisor.NewCallbackRegistry()
	addr, cleanup := startCallbackServer(t, f, reg)
	defer cleanup()

	status, body := postCallback(t, addr, uuid.NewString(), map[string]any{"type": "complete"})
	require.Equal(t, http.StatusNotFound, status)
	require.Contains(t, string(body), "unknown_async_ack_id")
}

func TestCallback_Complete_AppliesCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "producer", Executor: "worker"}})
	reg := supervisor.NewCallbackRegistry()
	addr, cleanup := startCallbackServer(t, f, reg)
	defer cleanup()

	ctx := context.Background()
	producer := f.addRunningNode("producer", "worker")
	rid, res := f.buildInlineResource(producer.ID, nil)
	dispatch := f.enqueueAndClaim(producer.ID, "worker", "sup-async")

	ackID := uuid.NewString()
	reg.Register(ackID, supervisor.AsyncContext{
		NodeID:       producer.ID,
		InstanceID:   f.instance,
		DispatchID:   dispatch.ID,
		SupervisorID: "sup-async",
		GetResource:  resolver(map[shared.UUID]resource.Resource{rid: res}),
	})

	status, body := postCallback(t, addr, ackID, map[string]any{
		"type":           "complete",
		"result":         map[string]any{"rows": []any{"a"}},
		"changed":        true,
		"change_summary": "async-ok",
	})
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(body), "accepted")

	got, err := f.sb.Nodes().Get(ctx, producer.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State)

	kinds := f.eventKinds(producer.ID)
	require.True(t, containsString(kinds, "commit"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)

	// Dispatch row was cleaned up.
	require.Nil(t, f.pendingDispatchForNode(producer.ID))

	// Ack was popped — re-posting should be 404.
	status2, _ := postCallback(t, addr, ackID, map[string]any{"type": "complete"})
	require.Equal(t, http.StatusNotFound, status2)
}

func TestCallback_Errored_RoutesOnError(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{
			Type: "worker", Executor: "worker",
			ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
				"boom": {Policy: []nodepkg.PolicyAction{
					{Action: "retry", Count: 1, Backoff: shared.BackoffLinear, BaseDelayMs: 50, MaxDelayMs: 50},
					{Action: "give_up"},
				}},
			},
		},
	})
	reg := supervisor.NewCallbackRegistry()
	addr, cleanup := startCallbackServer(t, f, reg)
	defer cleanup()

	ctx := context.Background()
	n := f.addRunningNode("worker", "worker")
	// For errored path, OnError.retry re-enqueues via RemoveForNode+Enqueue, so
	// the initial claimed row is removed — we don't need Complete to succeed.
	dispatch := f.enqueueAndClaim(n.ID, "worker", "sup-async")

	ackID := uuid.NewString()
	reg.Register(ackID, supervisor.AsyncContext{
		NodeID:       n.ID,
		InstanceID:   f.instance,
		DispatchID:   dispatch.ID,
		SupervisorID: "sup-async",
		GetResource: func(_ context.Context, _ shared.UUID) (resource.Resource, error) {
			return nil, fmt.Errorf("no resources")
		},
	})

	status, _ := postCallback(t, addr, ackID, map[string]any{
		"type":        "errored",
		"error_class": "boom",
		"payload":     map[string]any{"detail": "kaboom"},
	})
	require.Equal(t, http.StatusOK, status)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)
	require.Equal(t, "boom", got.CurrentErrorClass)
	require.Equal(t, 1, got.RetryCounter)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)

	// Retry re-enqueued a fresh dispatch row with a future enqueued_at.
	dr := f.pendingDispatchForNode(n.ID)
	require.NotNil(t, dr)
	require.WithinDuration(t, f.clock.Now().Add(50*time.Millisecond), dr.EnqueuedAt, 20*time.Millisecond)
}

func TestCallback_Blocked_RoutesExecutorBlocked(t *testing.T) {
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
	reg := supervisor.NewCallbackRegistry()
	addr, cleanup := startCallbackServer(t, f, reg)
	defer cleanup()

	ctx := context.Background()
	n := f.addRunningNode("worker", "worker")
	dispatch := f.enqueueAndClaim(n.ID, "worker", "sup-async")

	ackID := uuid.NewString()
	reg.Register(ackID, supervisor.AsyncContext{
		NodeID:       n.ID,
		InstanceID:   f.instance,
		DispatchID:   dispatch.ID,
		SupervisorID: "sup-async",
		GetResource: func(_ context.Context, _ shared.UUID) (resource.Resource, error) {
			return nil, fmt.Errorf("no resources")
		},
	})

	status, _ := postCallback(t, addr, ackID, map[string]any{
		"type":    "blocked",
		"reason":  "waiting on human",
		"context": map[string]any{"who": "ops"},
	})
	require.Equal(t, http.StatusOK, status)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got.State)

	// error event (error_class=executor_blocked) was written.
	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}
