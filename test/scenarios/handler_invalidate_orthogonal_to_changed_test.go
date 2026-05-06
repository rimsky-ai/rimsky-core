// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 42 — handler_invalidate_orthogonal_to_changed.
//
// Per spec §3.5, the handler.invalidate emit is orthogonal to resolve.
// A worker with on_executor_complete: { resolve: by_changed,
// invalidate: { targets: [monitor], frame: next } } that commits with
// changed=false must:
//   - record last_outcome=fresh_unchanged (no cascade to its own deps);
//   - still fire the invalidate emit, marking monitor stale.
//
// monitor is an independent node with no upstream so its only path back
// to stale is the handler.invalidate emit.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestHandlerInvalidateOrthogonalToChanged(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// worker commits no-op (changed=false). monitor commits truthy.
	h.Stub.WhenType("worker").Complete(map[string]any{}, false, "noop")
	h.Stub.WhenType("monitor").Complete(map[string]any{"m": 1}, true, "monitored")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "handler-invalidate-orthogonal", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				OnExecutorComplete: &node.OnExecutorCompleteHandler{
					Resolve: node.ResolveByChanged,
					Invalidate: &node.HandlerInvalidate{
						Targets: []string{"monitor"},
						Frame:   node.FrameNext,
					},
				},
			}),
			// monitor has no upstream — its only path back to stale is
			// the handler.invalidate emit.
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "monitor",
				Executor: "stub",
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-orthogonal", map[string]any{})

	worker := h.FindNode(iid, "worker")
	monitor := h.FindNode(iid, "monitor")
	require.NotNil(t, worker)
	require.NotNil(t, monitor)

	// Wait for both to reach fresh on first run.
	require.True(t, h.WaitForNodeState(monitor.ID, shared.NodeStateFresh, 30*time.Second),
		"monitor did not reach fresh on first run")
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 30*time.Second),
		"worker did not reach fresh on first run")

	// worker's last_outcome must be fresh_unchanged (its commit was a
	// no-op).
	require.True(t, waitForLastOutcome(t, h, worker.ID, shared.LastOutcomeFreshUnchanged, 30*time.Second),
		"worker should record last_outcome=fresh_unchanged on no-op commit")

	// Despite worker's no-op commit, the handler.invalidate must have
	// marked monitor stale and re-driven it to fresh. Count the number
	// of work_completed events for monitor — must be at least 2 (the
	// initial run + the handler-invalidate-driven re-run).
	require.True(t, waitForEventCount(t, h, monitor.ID, "work_completed", 2, 30*time.Second),
		"monitor must have run twice — once initially and once via handler.invalidate emit")

	// Verify the worker's last_outcome is still fresh_unchanged after the
	// orthogonal invalidate (the invalidate didn't cascade back to worker).
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, shared.LastOutcomeFreshUnchanged, wRow.LastOutcome,
		"worker's last_outcome should remain fresh_unchanged")
	require.Equal(t, shared.NodeStateFresh, wRow.State,
		"worker should be fresh")
}

// waitForEventCount polls rimsky_events for the count of (node_id, kind)
// rows. Returns true once the count meets or exceeds want.
func waitForEventCount(t *testing.T, h *scenario.Harness, nodeID shared.UUID, kind string, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		_ = h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = $2`,
			nodeID, kind).Scan(&count)
		if count >= want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
