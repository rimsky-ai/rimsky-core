// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestRetryLoopCapForcesGiveUp(t *testing.T) {
	t.Parallel()
	maxRetries := 3
	h := scenario.Start(t, scenario.HarnessOpts{})
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

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 60*time.Second),
		"worker should land in failed once retry-loop cap is reached")

	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		latest = r
		return err
	}))
	require.NotNil(t, latest)
	require.NotNil(t, latest.SettlingSignalType)
	require.Contains(t, *latest.SettlingSignalType, "terminal/error/",
		"give_up should record settling_signal_type=terminal/error/<class>")
}

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
				MaxRetriesWithoutProgress: &zero,
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/flaky": {Policy: []node.PolicyAction{{Action: "retry", Count: 5}}},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-loop-cap-zero", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	workerID := worker.ID
	eventwait.WaitForEvent(h.Ctx, t, h.Persist, eventwait.Matcher{
		NodeID: &workerID, KindPrefix: "transient/retry/", MinCount: 3,
	}, 30*time.Second)

	require.False(t, h.WaitForEventKind(worker.ID, "terminal/error/retry_loop_no_progress", 1*time.Second),
		"with cap=0, the runner must not emit terminal/error/retry_loop_no_progress")
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
