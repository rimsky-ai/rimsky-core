// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 24 — reactive_loop_self_invalidate_next_frame.
//
// A single-node template with:
//   - executor: stub
//   - a queue-shape claim against a stub claim-producer (3 items)
//   - on_executor_complete: { invalidate: { targets: [self], frame: next } }
//   - on_acquire_unavailable: { resolve: pass }
//
// Expected behavior across the run:
//   - The first 3 frames each acquire one queue item, run the executor,
//     commit, and self-invalidate the next frame.
//   - The 4th frame opens against a drained queue; the producer returns
//     Unavailable; on_acquire_unavailable: pass fires, transitioning
//     the node to fresh+passed.
//   - The instance ultimately reaches a quiescent state with the worker
//     in fresh state and last_outcome = passed.
package scenarios

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
	"github.com/fallguy/rimsky/stores/common/action"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

func TestReactiveLoopSelfInvalidateNextFrame(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				InitialItems: []json.RawMessage{
					json.RawMessage(`{"i":1}`),
					json.RawMessage(`{"i":2}`),
					json.RawMessage(`{"i":3}`),
				},
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Complete(map[string]any{"v": 1}, true, "ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "reactive-loop-next-frame", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					OnExecutorComplete: &node.OnExecutorCompleteHandler{
						Invalidate: &node.HandlerInvalidate{
							Targets: []string{node.SelfTarget},
							Frame:   node.FrameNext,
						},
					},
					OnAcquireUnavailable: &node.OnAcquireUnavailableHandler{
						Resolve: node.ResolvePass,
					},
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-reactive-next", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait until the queue is fully drained AND the on_acquire_unavailable
	// path has fired pass (last_outcome=passed).
	require.True(t, waitForLastOutcome(t, h, worker.ID, shared.LastOutcomePassed, 60*time.Second),
		"worker should land in fresh+passed once the queue is drained")

	// Verify final node state.
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, shared.NodeStateFresh, wRow.State,
		"worker should be fresh after the loop terminates via pass")

	// Stub producer should have observed at least 4 Open calls
	// (3 acquired + 1 unavailable). It also fires 3 Commits.
	var openCount, commitCount int
	for _, c := range sub.Calls() {
		switch c.Verb {
		case "open":
			openCount++
		case "commit":
			commitCount++
		}
	}
	require.GreaterOrEqual(t, openCount, 4,
		"expected at least 4 Open calls (3 acquired + 1 unavailable); got %d", openCount)
	require.Equal(t, 3, commitCount,
		"expected exactly 3 Commit calls (one per drained queue item); got %d", commitCount)

	// Producer's queue should be empty.
	require.Equal(t, 0, sub.QueueLen("@queue"),
		"queue should be drained at end of loop")

	// Verify at least 4 frames opened for this instance.
	var frameCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1`,
		iid).Scan(&frameCount))
	require.GreaterOrEqual(t, frameCount, 4,
		"expected at least 4 frames (3 commits + 1 pass); got %d", frameCount)
}
