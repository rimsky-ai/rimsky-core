// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestGiveUp(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("flaky").Error("my_err", map[string]any{"hint": "boom"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-give-up", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "flaky", Executor: "stub",
				MaxRetries: node.IntPtr(2),
				RetryBackoff: &node.RetryBackoffConfig{
					Kind:        shared.BackoffExponential,
					BaseDelayMs: 50,
					MaxDelayMs:  200,
				},
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/my_err": {Action: "retry"},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-giveup", map[string]any{})

	n := h.FindNode(iid, "flaky")
	require.NotNil(t, n)

	h.WaitForNodeState(n.ID, cascade.NodeStateFailed)

	var retryEventCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'transient/retry/%'`,
		[]any{n.ID}, &retryEventCount,
	)
	require.Equal(t, 2, retryEventCount,
		"give_up must exhaust exactly MaxRetries=2 transient/retry/<n>/<class> audit rows before failing")
}
