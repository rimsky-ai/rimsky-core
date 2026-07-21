// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: error-policy
func TestRetryLoopCapForcesGiveUp(t *testing.T) {
	t.Parallel()
	maxRetries := 3
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("flaky", map[string]any{"why": "nondeterministic"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-loop-cap", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:       "worker",
				Executor:   "stub",
				MaxRetries: &maxRetries,
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/flaky": {Action: "retry"},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-loop-cap", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.WaitForNodeState(worker.ID, cascade.NodeStateFailed)

	retries := eventwait.Events(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &worker.ID, KindPrefix: "transient/retry/"})
	require.Len(t, retries, maxRetries,
		"give_up must fire only after exactly maxRetries retry attempts, not on the first error")

	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, worker.ID, tx)
		latest = r
		return err
	}))
	require.NotNil(t, latest)
	require.NotNil(t, latest.SettlingSignalType)
	require.Contains(t, *latest.SettlingSignalType, "terminal/error/",
		"give_up should record settling_signal_type=terminal/error/<class>")
}
