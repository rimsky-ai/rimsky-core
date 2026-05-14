// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 2 — one pure-cascade node (no executor, no deps) is invalidated
// via the control API and transitions fresh → stale → fresh inline.
//
// Migrated to the stores-redesign template grammar (spec §11): the node is
// built via scenario.MakeNode. A pure-cascade node carries no executor,
// stores, locks, or attributes; the redesign treats this as a degenerate
// node — the scheduler's pure-cascade sweep promotes it to fresh once its
// (empty) dependency set is fresh.
package scenarios

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestPureCascadeNode(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pure-cascade", Version: "1",
		Nodes: []node.TemplateNodeDef{
			// No executor → pure-cascade node. No stores, locks, or
			// attributes wiring is required; the scheduler sweep promotes
			// it to fresh on the first tick.
			scenario.MakeNode(node.TemplateNodeDef{Type: "hub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-pc", map[string]any{})

	hub := h.FindNode(iid, "hub")
	require.NotNil(t, hub)
	// Starts stale; pure-cascade sweep should promote it to fresh on the
	// first scheduler tick.
	require.True(t, h.WaitForNodeState(hub.ID, cascade.NodeStateFresh, 10*time.Second),
		"hub did not reach fresh via initial pure-cascade sweep")

	// Invalidate via control API.
	resp, err := http.Post(h.ControlBase+"/nodes/"+hub.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Expect fresh again after next tick.
	require.True(t, h.WaitForNodeState(hub.ID, cascade.NodeStateFresh, 10*time.Second),
		"hub did not return to fresh after invalidate")

	// Verify pure_cascade_commit event was emitted at some point.
	nid := hub.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 500}, tx)
		evs = r
		return err
	}))
	var sawCommit bool
	for _, e := range evs.Events {
		if e.Kind == "pure_cascade_commit" {
			sawCommit = true
			break
		}
	}
	require.True(t, sawCommit, "expected pure_cascade_commit event")
}
