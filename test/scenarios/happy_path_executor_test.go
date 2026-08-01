// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHappyPathExecutor(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "initial")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "happy-path", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-happy", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 200}, tx)
		evs = r
		return err
	}))
	var sawCompleted bool
	for _, e := range evs.Events {
		if e.KindRaw == "terminal/success" {
			sawCompleted = true
			break
		}
	}
	require.True(t, sawCompleted, "expected terminal/success signal event")

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, n.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "expected node_attributes row to exist after commit")
	require.Equal(t, true, row.Data["ok"],
		"expected attributes.data.ok = true from executor's delta")
}
