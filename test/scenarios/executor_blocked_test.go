// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 10 — executor emits Blocked; supervisor classifies the outcome
// as error_class="executor_blocked" and evaluates policy.
//
// Migrated to the stores-redesign template grammar (spec §11): the gated
// node is built via scenario.MakeNode. The node has no stores, locks, or
// attributes wiring — Blocked terminates without writing back attributes,
// so a schema-less node is the right shape; the redesign retains the
// per-error-class policy chain (spec §11.6) the test exercises.
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

func TestExecutorBlocked(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("gated").Blocked("stuck", map[string]any{"need": "input"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "blocked", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "gated", Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"executor_blocked": {
						Policy: []node.PolicyAction{
							{Action: "give_up"},
						},
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-blocked", map[string]any{})

	n := h.FindNode(iid, "gated")
	require.NotNil(t, n)

	// give_up on executor_blocked → node fails.
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFailed, 20*time.Second),
		"gated did not reach failed")

	// Verify error event carries executor_blocked class.
	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid, Kind: "error"},
			persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	}))
	var found bool
	for _, e := range evs.Events {
		if cls, _ := e.Payload["error_class"].(string); cls == "executor_blocked" {
			found = true
			break
		}
	}
	require.True(t, found, "expected error event with error_class=executor_blocked")
}
