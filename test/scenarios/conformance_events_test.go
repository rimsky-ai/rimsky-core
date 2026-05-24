// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// L3 conformance test: rimsky_node_events ledger semantics. Asserts that
// events emitted via the gRPC stream are persisted, substitution from
// `nodes.<emitter>.event.<name>.<path>` returns the expected payload
// value, and multiple emissions return the most recent.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

// TestConformanceEvents covers the L3 conformance shape. A emits two
// NamedEvents; the latest persists; B substitutes from the ledger via
// `nodes.a.event.ready.field` and the value reaches B's resolved
// attributes.
func TestConformanceEvents(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// A emits the same event name twice; the second should win for
	// substitution.
	h.Stub.WhenType("a").
		EmitNamedEvent("ready", []byte(`{"value":"first"}`)).
		EmitNamedEvent("ready", []byte(`{"value":"second"}`)).
		Success(map[string]any{}, true, "a-done")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b-done")

	// B's attributes substitute from A's most-recent event payload via
	// the F4 source kind `nodes.a.event.ready.value`.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "conformance-events", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "a",
				Executor: "stub",
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				// The {{nodes.a.event.ready.value}} ref below auto-subscribes
				// b to a's `event` topic with name="ready"; explicit
				// subscription kept here for clarity.
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "event/ready"}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"signal": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.event.ready.value}}",
						},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-conformance-events", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should complete")

	// Ledger should have at least one row for (a, ready); LatestByName
	// returns the most recent.
	require.Eventually(t, func() bool {
		evt, err := getLatestNodeEvent(t, h, iid, a.ID, "ready")
		return err == nil && evt != nil && len(evt.PayloadInline) > 0
	}, 10*time.Second, 100*time.Millisecond, "ledger row should be visible")

	evt, err := getLatestNodeEvent(t, h, iid, a.ID, "ready")
	require.NoError(t, err)
	require.NotNil(t, evt)
	require.Contains(t, string(evt.PayloadInline), `"value":"second"`,
		"LatestByName must return the most recent emission")

	// B should run with the substituted value visible in its attributes.
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b should run after A's event invalidates it")

	var attrs *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		row, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, b.ID, h.GetMainRunScopeID(iid), tx)
		attrs = row
		return err
	}))
	require.NotNil(t, attrs, "b should have attributes after run")
	require.Equal(t, "second", attrs.Data["signal"],
		"b's substituted attribute should reflect the most-recent event payload")
}
