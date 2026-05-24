// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario test for the `hard_dep: true` attribute schema flag under
// per-run keying. When receiver C declares `hard_dep: true` on
// `{{nodes.B.attribute.foo}}`, an invalidation of C also invalidates
// B in the same frame so B's value is freshly produced.
//
// Per spec
// .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
// §"hard-dep cascade extension".
package per_run_attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

// TestPerRunAttributes_HardDepPullsUpstream verifies the hard-dep
// cascade extension: when A transitions and C subscribes to A, the
// cascade walker also proactively invalidates B (because C declares
// `hard_dep: true` on B's attribute). Then both A and B settle in the
// same frame; C dispatches reading both.
func TestPerRunAttributes_HardDepPullsUpstream(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-1"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-hard-dep", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_value": map[string]any{"type": "string"},
					},
					"required": []any{"a_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b_value": map[string]any{"type": "string"},
					},
					"required": []any{"b_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", On: "state"}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.a_value}}",
						},
						"b_val": map[string]any{
							"type":     "string",
							"source":   "{{nodes.b.attribute.b_value}}",
							"hard_dep": true,
						},
					},
					"required": []any{"a_val", "b_val"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-hard-dep", map[string]any{})
	aN := h.FindNode(iid, "a")
	bN := h.FindNode(iid, "b")
	cN := h.FindNode(iid, "c")
	require.NotNil(t, aN)
	require.NotNil(t, bN)
	require.NotNil(t, cN)

	// Initial frame: A, B, C all run.
	require.True(t, h.WaitForNodeState(cN.ID, cascade.NodeStateFresh, 15*time.Second),
		"c should reach fresh after hard-dep cascade")

	var cRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetMainRunScopeID(iid), tx)
		cRow = r
		return err
	}))
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-1", cRow.Data["a_val"], "c should see a's first-fire value")
	require.Equal(t, "from-b-1", cRow.Data["b_val"], "c should see b's first-fire value (hard-dep pulled)")

	// Re-prime stubs for second fire.
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-2"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	// Invalidate A. The cascade should pull B (hard-dep) and re-fire C.
	adminInvalidate(t, h, iid, aN.ID)

	// Wait until C's latest attribute row reflects both A's and B's second-fire values.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetMainRunScopeID(iid), tx)
			cRow = r
			return err
		})
		if cRow != nil && cRow.Data["a_val"] == "from-a-2" && cRow.Data["b_val"] == "from-b-2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-2", cRow.Data["a_val"], "c should see a's second-fire value")
	require.Equal(t, "from-b-2", cRow.Data["b_val"],
		"c should see b's second-fire value (hard-dep cascade re-fired b)")
}

// Direct coverage of the hard-dep parked-upstream wake lives in the
// unit test `runtime.TestPullHardDepUpstreams_WakesParkedUpstream`
// (file:runtime/hard_dep_cascade_test.go). It sets up a parked
// upstream via direct SQL and invokes the cascade walk in isolation,
// avoiding the scenario.Harness's race-prone timing constraints
// (the cascade walk's `ListByInstance` snapshot is taken before
// any concurrent park terminal commits, so an external
// `WaitForNodeState` poll cannot reliably sequence a "B parked → A's
// cascade walks" ordering through the harness).
