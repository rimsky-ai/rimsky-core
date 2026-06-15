// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 2 — one pure-cascade node (no executor, no deps) is invalidated
// via the runtime-synthetic envelope and transitions fresh → stale → fresh
// inline.
//
// Migrated to the stores-redesign template grammar (spec §11): the node is
// built via scenario.MakeNode. A pure-cascade node carries no executor,
// stores, locks, or attributes; the redesign treats this as a degenerate
// node — the scheduler's pure-cascade sweep promotes it to fresh once its
// (empty) dependency set is fresh.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestPureCascadeNode(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pure-cascade", Version: "1",
		Nodes: []node.TemplateNodeDef{
			// @deliberate: No executor → pure-cascade node. No stores, locks, or
			// attributes wiring is required; the scheduler sweep promotes
			// it to fresh on the first tick.
			scenario.MakeNode(node.TemplateNodeDef{Type: "hub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-pc", map[string]any{})

	hub := h.FindNode(iid, "hub")
	require.NotNil(t, hub)
	// @deliberate: Starts stale; pure-cascade sweep should promote it to fresh on the
	// first scheduler tick.
	require.True(t, h.WaitForNodeState(hub.ID, cascade.NodeStateFresh, 10*time.Second),
		"hub did not reach fresh via initial pure-cascade sweep")

	// @deliberate: Drive re-dispatch via the runtime-synthetic envelope.
	// The operator-invalidate HTTP route was retired with the typed-message
	// schema layer; admin invalidate now flows through the debug channel,
	// and the test harness wraps the same internal entrypoint.
	h.InvalidateNode(iid, hub.ID)

	// @deliberate: Expect fresh again after next tick.
	require.True(t, h.WaitForNodeState(hub.ID, cascade.NodeStateFresh, 10*time.Second),
		"hub did not return to fresh after invalidate")

	// @constraint: Verify a terminal/success signal event was emitted (per Pass 5
	// the pure_cascade_commit fixed-string row retired; pure-cascade
	// transitions emit terminal/success with payload.change_summary
	// = "pure_cascade" per concept:signal).
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
		if e.KindRaw == "terminal/success" {
			sawCommit = true
			break
		}
	}
	require.True(t, sawCommit, "expected terminal/success signal event for pure-cascade transition")
}
