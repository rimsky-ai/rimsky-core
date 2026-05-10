// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// E5 retry-loop cap scenario tests. The runner forces an Errored
// terminal with error_class="retry_loop_no_progress" once
// consecutive_retries_no_progress reaches the effective cap. Per the
// 2026-05-08 platform-extensions plan E6 (retry section).

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

// TestRetryLoopCapForcesGiveUp covers E6 retry case (a). When the
// per-row consecutive-retries counter reaches the effective cap, the
// runner forces give_up. We use a very small per-node cap (3) so the
// test runs quickly.
func TestRetryLoopCapForcesGiveUp(t *testing.T) {
	t.Parallel()
	maxRetries := 3
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Stub returns an error class with a retry policy. The retry counter
	// increments each retry; after maxRetries+1 retries the runner forces
	// retry_loop_no_progress → give_up.
	h.Stub.WhenType("worker").Error("flaky", map[string]any{"why": "nondeterministic"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-loop-cap", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:                      "worker",
				Executor:                  "stub",
				MaxRetriesWithoutProgress: &maxRetries,
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"flaky": {Policy: []node.PolicyAction{{Action: "retry", Count: 1000}}},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-loop-cap", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Should reach failed with error_class=retry_loop_no_progress within
	// some seconds (each retry round-trips the executor + policy chain).
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFailed, 60*time.Second),
		"worker should land in failed once retry-loop cap is reached")

	// Confirm the LastOutcome is failed (give_up's outcome).
	var row *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, shared.LastOutcomeFailed, row.LastOutcome,
		"give_up should record last_outcome=failed")
}

// TestRetryLoopCapDisabledWithZero covers E6 retry case (c). A per-node
// override of 0 disables the cap entirely; the node retries indefinitely
// without being force-failed.
//
// The test confirms the runner does NOT force give_up when the override
// is 0: after several retries, the node remains in the retry loop (state
// stale or running, not failed) and no retry_loop_no_progress event is
// recorded.
func TestRetryLoopCapDisabledWithZero(t *testing.T) {
	t.Parallel()
	zero := 0
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("flaky", map[string]any{"why": "no-cap"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-loop-cap-zero", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:                      "worker",
				Executor:                  "stub",
				MaxRetriesWithoutProgress: &zero, // 0 = cap disabled
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"flaky": {Policy: []node.PolicyAction{{Action: "retry", Count: 5}}},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-loop-cap-zero", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait long enough for several retries to elapse. The node should
	// either still be cycling (stale/running) or give up via the standard
	// retry-budget exhaustion (Count: 5) — but not via the retry-loop cap.
	time.Sleep(5 * time.Second)

	// Verify: no retry_loop_no_progress event was emitted on this node.
	require.False(t, h.WaitForEventKind(worker.ID, "retry_loop_no_progress", 1*time.Second),
		"with cap=0, the runner must not emit retry_loop_no_progress")
	// And no error event whose payload mentions retry_loop_no_progress.
	var rows []map[string]any
	h.QuerySQL(
		`SELECT payload::text FROM rimsky_events WHERE node_id = $1 AND kind = 'error'`,
		[]any{worker.ID},
		func(scan func(...any) error) error {
			var raw []byte
			if err := scan(&raw); err != nil {
				return err
			}
			rows = append(rows, map[string]any{"payload": string(raw)})
			return nil
		},
	)
	for _, r := range rows {
		require.NotContains(t, r["payload"], "retry_loop_no_progress",
			"cap=0 must disable the retry-loop force-give_up branch")
	}
}
