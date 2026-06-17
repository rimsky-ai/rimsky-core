// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 2 — one pure-cascade node (no executor, no deps) is invalidated
// via a per-target typed-message wake (the template declares a
// `test/wake/hub` message; the node subscribes to it; the test body
// emits an envelope of that type) and transitions fresh → stale →
// fresh inline.
//
// @decision: empty-message-as-root-trigger
// @story: empty-message-wakes-roots
//
// Migrated to the stores-redesign template grammar (spec §11): the node is
// built via scenario.MakeNode. A pure-cascade node carries no executor,
// stores, locks, or attributes; the redesign treats this as a degenerate
// node — the scheduler's pure-cascade sweep promotes it to fresh once its
// (empty) dependency set is fresh.
package scenarios

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestPureCascadeNode(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pure-cascade", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/hub"},
		},
		Nodes: []node.TemplateNodeDef{
			// @deliberate: No executor → pure-cascade node. No stores, locks, or
			// attributes wiring is required; the scheduler sweep promotes
			// it to fresh on the first tick.
			scenario.MakeNode(node.TemplateNodeDef{Type: "hub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/hub", Type: "terminal/success",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-pc", map[string]any{})

	hub := h.FindNode(iid, "hub")
	require.NotNil(t, hub)
	// @constraint: hub was previously a structural root; the
	// subscribes: entry added for the typed-message wake demoted it
	// from root, so the harness's empty-wake doesn't fire it. Emit
	// the typed message here to drive the initial dispatch the test
	// assertions expect.
	h.PostInstanceMessage(iid, "test/wake/hub", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	// @deliberate: The typed-message wake fires hub via the cascade
	// walker; hub then settles to fresh per the no-executor pure-
	// cascade transition.
	require.True(t, h.WaitForNodeState(hub.ID, cascade.NodeStateFresh, 10*time.Second),
		"hub did not reach fresh after the typed-message wake")

	// @deliberate: Drive re-dispatch via typed-message wake.
	h.PostInstanceMessage(iid, "test/wake/hub", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

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
