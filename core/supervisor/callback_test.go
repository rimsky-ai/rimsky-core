// Tests for `CallbackServer` — the §12.4 async-handoff terminal callback
// endpoint. Each test boots a fixture, registers an `AsyncContext`
// directly under a synthetic ackID, POSTs a §12.3 callback body to the
// endpoint, and asserts on the resulting node state + event audit trail.
//
// The §12.5 incremental-attributes endpoint is covered separately in
// `core/attributes/callback_test.go` (Task 9). This file tests the
// terminal-handoff flow only.
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
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/supervisor"
)

// startCallbackServer builds a CallbackServer wired to the fixture's
// pgxpool, lock-holders client, and store registry, and starts it on an
// OS-assigned port. The §17.1 step 6c release tx runs on QueuePool +
// LockHolders, so both fields are required for the Complete branch to
// run cleanly.
func startCallbackServer(t *testing.T, f *fixture, reg *supervisor.CallbackRegistry) (string, func()) {
	t.Helper()
	srv := &supervisor.CallbackServer{
		Registry:    reg,
		Storage:     f.sb,
		Queue:       f.q,
		QueuePool:   f.pool,
		LockHolders: f.lockHolders,
		Clock:       f.clock,
		Logger:      f.log,
	}
	addr, err := srv.Start("127.0.0.1", 0)
	require.NoError(t, err)
	return addr, func() {
		_ = srv.Close(context.Background())
	}
}

// postCallback sends a JSON body to /v1/callback/{async_ack_id} and
// returns (status, body). Errors fail the test.
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

// makeAsyncContext builds an AsyncContext with the new redesign fields
// populated for a node that holds no locks (the simplest case — the
// release loop becomes a no-op walk over an empty AcquiredLocks slice).
// Used by the Errored / Blocked tests where the policy chain handles
// state transition + queue mutation but no Store.Commit / ReleaseLock
// must be invoked.
func makeAsyncContext(
	f *fixture, supervisorID string, n storage.NodeRow, dispatchID shared.UUID,
) supervisor.AsyncContext {
	frameID := shared.UUID{}
	if n.FrameID != nil {
		frameID = *n.FrameID
	}
	return supervisor.AsyncContext{
		NodeID:        n.ID,
		InstanceID:    f.instance,
		DispatchID:    dispatchID,
		SupervisorID:  supervisorID,
		NodeType:      n.NodeType,
		Executor:      n.Executor,
		StoreRegistry: f.registry,
		FrameID:       frameID,
		// AcquiredLocks empty — Complete-branch tests still pass
		// because the lockless release walk is a no-op; Blocked /
		// Errored branches don't consult locks either.
		AcquiredLocks: nil,
		// NodeDef is loaded by the Complete-branch quality-rule check
		// and the Errored-branch policy lookup. We resolve it here
		// directly from the template so tests don't rely on field-
		// hidden state.
		NodeDef: f.nodeDefFor(n.NodeType),
	}
}

// nodeDefFor walks the fixture's template and returns the per-node-type
// def, or nil. Convenience for tests building AsyncContexts.
func (f *fixture) nodeDefFor(nodeType string) *nodepkg.TemplateNodeDef {
	f.t.Helper()
	tpl, err := f.sb.Templates().Get(context.Background(), f.template, nil)
	require.NoError(f.t, err)
	if tpl == nil {
		return nil
	}
	for i := range tpl.Spec.Nodes {
		if tpl.Spec.Nodes[i].Type == nodeType {
			cp := tpl.Spec.Nodes[i]
			return &cp
		}
	}
	return nil
}

// enqueueClaimedDispatch inserts a dispatch row pointing at nodeID,
// then claims it under supervisorID via the queue's two-step
// SelectCandidates + ClaimDispatchRow primitives. Returns the claimed
// dispatch ID.
//
// We use the building-block primitives directly rather than the runner
// because the runner does the full §13.3 acquisition flow (state
// transitions, lock-holder inserts, etc.) — these tests only need the
// dispatch row to be marked claimed_by=supervisorID for the callback
// path.
func (f *fixture) enqueueClaimedDispatch(nodeID shared.UUID, executorName, supervisorID string) shared.UUID {
	f.t.Helper()
	ctx := context.Background()
	n, err := f.sb.Nodes().Get(ctx, nodeID, nil)
	require.NoError(f.t, err)
	require.NotNil(f.t, n.FrameID, "enqueueClaimedDispatch requires node frame_id")
	require.NoError(f.t, f.q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       nodeID,
		ExecutorName: executorName,
		EnqueuedAt:   f.clock.Now(),
		FrameID:      *n.FrameID,
	}))
	dispatchID := f.directClaim(nodeID, supervisorID)
	return dispatchID
}

// directClaim looks up the pending dispatch row by node ID and updates
// claimed_by to supervisorID. Used by callback tests that need a row
// with the claim-fields set without going through the full runner.
func (f *fixture) directClaim(nodeID shared.UUID, supervisorID string) shared.UUID {
	f.t.Helper()
	ctx := context.Background()
	var id shared.UUID
	err := f.pool.QueryRow(ctx,
		`SELECT id FROM rimsky_dispatch WHERE node_id = $1`, nodeID,
	).Scan(&id)
	require.NoError(f.t, err)
	_, err = f.pool.Exec(ctx,
		`UPDATE rimsky_dispatch
		   SET claimed_by = $1, claimed_at = now(), last_heartbeat_at = now()
		 WHERE id = $2`,
		supervisorID, id)
	require.NoError(f.t, err)
	return id
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

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

// TestCallback_Complete_AppliesCommit covers the Complete-branch happy
// path. The acquired-locks slice is empty — the §17.1 step 6c release
// loop walks zero rows, the per-tx state transition runs, and the node
// lands in fresh.
func TestCallback_Complete_AppliesCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "producer", Executor: "worker"}})
	reg := supervisor.NewCallbackRegistry()
	addr, cleanup := startCallbackServer(t, f, reg)
	defer cleanup()

	ctx := context.Background()
	producer := f.addRunningNode("producer", "worker")
	dispatchID := f.enqueueClaimedDispatch(producer.ID, "worker", "sup-async")

	ackID := uuid.NewString()
	reg.Register(ackID, makeAsyncContext(f, "sup-async", producer, dispatchID))

	status, body := postCallback(t, addr, ackID, map[string]any{
		"type":             "complete",
		"changed":          true,
		"change_summary":   "async-ok",
		"attributes_delta": map[string]any{"rows": []any{"a"}},
	})
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(body), "accepted")

	got, err := f.sb.Nodes().Get(ctx, producer.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State)

	kinds := f.eventKinds(producer.ID)
	require.True(t, containsString(kinds, "attributes_committed"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)

	// Dispatch row was cleaned up by Queue.Complete after driveTerminal.
	require.Nil(t, f.pendingDispatchForNode(producer.ID))

	// Ack was popped — re-posting should be 404.
	status2, _ := postCallback(t, addr, ackID, map[string]any{"type": "complete"})
	require.Equal(t, http.StatusNotFound, status2)
}

// TestCallback_Errored_RoutesPolicyChain covers the Errored branch.
// Policy chain: retry once with linear backoff, then give_up. The first
// occurrence routes through retry → state stale + re-enqueued dispatch.
func TestCallback_Errored_RoutesPolicyChain(t *testing.T) {
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
	dispatchID := f.enqueueClaimedDispatch(n.ID, "worker", "sup-async")

	ackID := uuid.NewString()
	reg.Register(ackID, makeAsyncContext(f, "sup-async", n, dispatchID))

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
	require.WithinDuration(t, f.clock.Now().Add(50*time.Millisecond), dr.EnqueuedAt, 100*time.Millisecond)
}

// TestCallback_Blocked_RoutesExecutorBlocked covers the Blocked branch:
// the body's `reason`+`context` are mapped to the synthetic
// `executor_blocked` error class. With an explicit give_up override the
// node lands in failed.
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
	dispatchID := f.enqueueClaimedDispatch(n.ID, "worker", "sup-async")

	ackID := uuid.NewString()
	reg.Register(ackID, makeAsyncContext(f, "sup-async", n, dispatchID))

	status, _ := postCallback(t, addr, ackID, map[string]any{
		"type":    "blocked",
		"reason":  "waiting on human",
		"context": map[string]any{"who": "ops"},
	})
	require.Equal(t, http.StatusOK, status)

	got, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, got.State)
	require.Equal(t, "executor_blocked", got.CurrentErrorClass)

	kinds := f.eventKinds(n.ID)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
}

// TestCallback_InvalidJSON_RegistersAndReturns400 ensures a malformed
// body does NOT consume the registered ackID — the executor can retry.
func TestCallback_InvalidJSON_RegistersAndReturns400(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{{Type: "worker", Executor: "worker"}})
	reg := supervisor.NewCallbackRegistry()
	addr, cleanup := startCallbackServer(t, f, reg)
	defer cleanup()

	n := f.addRunningNode("worker", "worker")
	dispatchID := f.enqueueClaimedDispatch(n.ID, "worker", "sup-async")
	ackID := uuid.NewString()
	reg.Register(ackID, makeAsyncContext(f, "sup-async", n, dispatchID))

	url := fmt.Sprintf("http://%s/v1/callback/%s", addr, ackID)
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte("{not-json")))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Ack still registered — a retry with valid JSON would be accepted.
	status, _ := postCallback(t, addr, ackID, map[string]any{"type": "complete", "changed": false})
	require.Equal(t, http.StatusOK, status)
}
