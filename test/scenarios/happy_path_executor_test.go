// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 1 — happy path: one executor-backed node runs to completion.
//
// Migrated to the stores-redesign template grammar (spec §11): nodes are
// built via scenario.MakeNode + the fluent option helpers. The redesign
// replaces the legacy "this resource has version N" assertion with
// "this node's rimsky_node_attributes.data contains field X"; this
// scenario keeps the original state-based assertion plus an attributes
// readback to demonstrate the new shape.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

func TestHappyPathExecutor(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Stub returns an attributes_delta containing {"ok": true}; the
	// supervisor merges it into the node's resolved attributes.
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
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	// Verify a terminal/success signal event was appended. Per Pass 5
	// the canonical audit row for a settled-fresh terminal is the
	// signal type-path; the legacy `work_completed` fixed-string row
	// retired.
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
		if e.Kind == "terminal/success" {
			sawCompleted = true
			break
		}
	}
	require.True(t, sawCompleted, "expected terminal/success signal event")

	// Verify the executor's attributes_delta landed in
	// rimsky_node_attributes.data — the redesign's replacement for
	// "resource has version N" assertions.
	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, n.ID, h.GetMainRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "expected node_attributes row to exist after commit")
	require.Equal(t, true, row.Data["ok"],
		"expected attributes.data.ok = true from executor's delta")
}
