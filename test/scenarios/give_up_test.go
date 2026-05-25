// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 6 — retry-then-give_up policy routes a persistently failing
// node to state=failed after exhausting retries.
//
// Migrated to the stores-redesign template grammar (spec §11): the flaky
// node is built via scenario.MakeNode. The node has no stores, locks, or
// attributes wiring — the test exercises the policy chain (spec §11.6)
// only; an erroring executor never produces an attributes_delta, so a
// schema-less node is the right shape.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
	"github.com/fallguyconsulting/rimsky/graph/shared"
)

func TestGiveUp(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Stub always errors with class "my_err".
	h.Stub.WhenType("flaky").Error("my_err", map[string]any{"hint": "boom"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-give-up", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "flaky", Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/my_err": {
						Policy: []node.PolicyAction{
							{Action: "retry", Count: 2, Backoff: shared.BackoffExponential, BaseDelayMs: 50, MaxDelayMs: 200},
							{Action: "give_up"},
						},
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-giveup", map[string]any{})

	n := h.FindNode(iid, "flaky")
	require.NotNil(t, n)

	// Eventually retries exhaust and node transitions to failed.
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFailed, 30*time.Second),
		"flaky did not reach failed after exhausting retries")
}
