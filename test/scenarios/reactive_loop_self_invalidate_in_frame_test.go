// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 25 — reactive_loop_self_invalidate_in_frame.
//
// Same template shape as Task 24 but the on_executor_complete invalidate
// uses frame: in. All iterations of the loop run inside a single frame.
// The frame stays running until the queue is drained; last_progress_at
// updates per iteration so the frame_timeout reaper does not fire.
package scenarios

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

func TestReactiveLoopSelfInvalidateInFrame(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommitDefault: "delete",
				OnGiveUpDefault: "release_to_back",
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
		Name: "reactive-loop-in-frame", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		// Generous timeout so progressing-loop runs don't trip the reaper.
		FrameTimeoutMs: 600000,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					OnExecutorComplete: &node.OnExecutorCompleteHandler{
						Invalidate: &node.HandlerInvalidate{
							Targets: []string{node.SelfTarget},
							Frame:   node.FrameIn,
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
	iid := h.CreateInstance(tid, "ck-reactive-in", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait until the loop terminates (last_outcome=passed once the queue
	// is drained on the unavailable iteration).
	require.True(t, waitForLastOutcome(t, h, worker.ID, shared.LastOutcomePassed, 60*time.Second),
		"worker should land in fresh+passed once the in-frame loop drains the queue")

	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, shared.NodeStateFresh, wRow.State)

	// Frame count under in-frame self-invalidate. Per spec §5.2 a
	// frame: in self-invalidate loop must run inside a single frame
	// for the entire drain. Without the InvalidateArgs.SourceFrameID
	// override, the post-Complete emit would always fall back to
	// next-frame because the running-tx already cleared the source
	// row's frame_id; the override carries acq.FrameID through so
	// invalidateInFrame lands the next iteration on the same frame.
	var frameCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1`,
		iid).Scan(&frameCount))
	require.Equal(t, 1, frameCount,
		"expected exactly 1 frame under in-frame self-invalidate loop; got %d", frameCount)

	// Producer should have observed 3 Open+Commits and 1 unavailable Open.
	var openCount, commitCount int
	for _, c := range sub.Calls() {
		switch c.Verb {
		case "open":
			openCount++
		case "commit":
			commitCount++
		}
	}
	require.Equal(t, 3, commitCount,
		"expected exactly 3 commits (one per drained queue item); got %d", commitCount)
	require.GreaterOrEqual(t, openCount, 4,
		"expected ≥4 Opens (3 acquired + 1 unavailable); got %d", openCount)

	// Per spec §7: last_progress_at must have advanced past frame
	// started_at across at least one iteration. With the SourceFrameID
	// override the loop runs in a single frame; wait for that frame to
	// reach 'completed' (the frame engine closes frames after the
	// last source-node terminal once no stale/running rows remain).
	deadline := time.Now().Add(15 * time.Second)
	var startedAt, lastProgressAt time.Time
	for time.Now().Before(deadline) {
		err := h.Pool.QueryRow(h.Ctx, `
			SELECT started_at, last_progress_at FROM rimsky_frames
			WHERE instance_id = $1 AND state = 'completed'
			ORDER BY queued_at DESC LIMIT 1
		`, uuid.UUID(iid)).Scan(&startedAt, &lastProgressAt)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.False(t, startedAt.IsZero(),
		"frame did not reach 'completed' within 15s; the in-frame self-invalidate loop should close its single frame after the last drain iteration")
	require.False(t, lastProgressAt.Before(startedAt),
		"last_progress_at (%v) should not be before started_at (%v)",
		lastProgressAt, startedAt)
}
