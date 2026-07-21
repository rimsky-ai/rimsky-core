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
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestGiveUpPolicyForExecutorDeclaredErrorClass(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("gated").Error("custom_failure", map[string]any{
		"reason": "stuck",
		"need":   "input",
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "executor-declared-error-give-up", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "gated", Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/custom_failure": {Action: "give_up"},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-executor-declared-error-give-up", map[string]any{})

	n := h.FindNode(iid, "gated")
	require.NotNil(t, n)

	h.WaitForNodeState(n.ID, cascade.NodeStateFailed)

	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid, KindIn: []string{"terminal/error/stub/custom_failure"}},
			persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	}))
	require.NotEmpty(t, evs.Events, "expected terminal/error/stub/custom_failure signal row")
}
