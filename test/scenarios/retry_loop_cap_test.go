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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestRetryLoopCapForcesGiveUp covers E6 retry case (a). When the
// per-row consecutive-retries counter reaches the effective cap, the
// runner forces give_up. We use a very small per-node cap (3) so the
// test runs quickly.
func TestRetryLoopCapForcesGiveUp(t *testing.T) {
	t.Parallel()
	maxRetries := 3
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: Stub returns an error class with a retry policy. The retry counter
	// increments each retry; after maxRetries+1 retries the runner forces
	// retry_loop_no_progress → give_up.
	h.Stub.WhenType("worker").Error("flaky", map[string]any{"why": "nondeterministic"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-loop-cap", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:                      "worker",
				Executor:                  "stub",
				MaxRetriesWithoutProgress: &maxRetries,
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/flaky": {Policy: []node.PolicyAction{{Action: "retry", Count: 1000}}},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-loop-cap", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// @deliberate: Should reach failed with error_class=retry_loop_no_progress within
	// some seconds (each retry round-trips the executor + policy chain).
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 60*time.Second),
		"worker should land in failed once retry-loop cap is reached")

	// @deliberate: Confirm the settling_signal_type carries the canonical
	// terminal/error/<class> envelope (give_up's settled disposition).
	var row *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.NotNil(t, row.SettlingSignalType)
	require.Contains(t, *row.SettlingSignalType, "terminal/error/",
		"give_up should record settling_signal_type=terminal/error/<class>")
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
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:                      "worker",
				Executor:                  "stub",
				MaxRetriesWithoutProgress: &zero, // @deliberate: 0 = cap disabled
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/flaky": {Policy: []node.PolicyAction{{Action: "retry", Count: 5}}},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-loop-cap-zero", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// @deliberate: Wait until several retries have actually elapsed. Each retry
	// emits a transient/retry/<n>/<class> audit row, so the event log
	// carries the durable record of "retries happened" — waiting on it
	// (instead of a fixed sleep) pins the precondition: the no-cap
	// assertions below are vacuous unless retries genuinely occurred,
	// and a fixed sleep can undershoot under load. With a per-node cap
	// override of 0 and a retry budget of 5, at least 3 retries must
	// land. (2026-06-11 polling audit: converted site.)
	workerID := worker.ID
	eventwait.WaitForEvent(h.Ctx, t, h.Persist, eventwait.Matcher{
		NodeID: &workerID, KindPrefix: "transient/retry/", MinCount: 3,
	}, 30*time.Second)

	// @deliberate: Verify: no terminal/error/retry_loop_no_progress signal was emitted
	// on this node. Post-Pass-5 the canonical signal taxonomy replaces
	// the legacy fixed-string `error` audit kind; the wildcard prefix
	// `terminal/error/` covers both give_up and pass terminal envelopes
	// for the audit-log scan below.
	require.False(t, h.WaitForEventKind(worker.ID, "terminal/error/retry_loop_no_progress", 1*time.Second),
		"with cap=0, the runner must not emit terminal/error/retry_loop_no_progress")
	// @deliberate: And no terminal/error/* audit row whose payload mentions
	// retry_loop_no_progress (covers the case where the class travels in
	// the payload — e.g. via original_error_class on the rewritten cap
	// envelope — rather than on the kind itself).
	var rows []map[string]any
	h.QuerySQL(
		`SELECT payload::text FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'terminal/error/%'`,
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
