// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario test for per-run attribute keying — sequential reruns
// persist as independent attribute rows.
//
// Two sequential runs of the same node, separated by an admin
// invalidate, must each persist their own attribute row keyed by
// their own `node_run_id`. Under the legacy per-node keying, the
// second run would overwrite the first; under per-run keying
// (2026-05-20) each row is independent and queryable by run id.
//
// Per spec
// .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md.
package per_run_attributes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

// TestPerRunAttributes_SequentialRunsTwoRows verifies that two
// consecutive runs of the same node persist into TWO independent
// attribute rows (keyed by node_run_id), rather than overwriting a
// single per-node row. Exercises the most fundamental per-run-keying
// invariant.
func TestPerRunAttributes_SequentialRunsTwoRows(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"value": "first"}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-two-runs", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string"},
					},
					"required": []any{"value"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-two-runs", map[string]any{})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)

	// First run.
	require.True(t, h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second))
	var first *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetMainRunScopeID(iid), tx)
		first = r
		return err
	}))
	require.NotNil(t, first)
	firstRunID := first.NodeRunID
	require.Equal(t, "first", first.Data["value"])

	// Re-prime stub for second run.
	h.Stub.WhenType("worker").Success(map[string]any{"value": "second"}, true, "ok")

	// Trigger a fresh run via admin invalidate.
	adminInvalidate(t, h, iid, w.ID)

	// Wait until the latest attribute row has a different run id and
	// reflects the second invocation's value.
	deadline := time.Now().Add(15 * time.Second)
	var latest *persistence.NodeAttributesRow
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetMainRunScopeID(iid), tx)
			latest = r
			return err
		})
		if latest != nil && latest.NodeRunID != firstRunID && latest.Data["value"] == "second" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, latest)
	require.NotEqual(t, firstRunID, latest.NodeRunID,
		"second run should have a different node_run_id")
	require.Equal(t, w.ID, latest.NodeID,
		"denormalized node_id stays consistent across runs")
	require.Equal(t, "second", latest.Data["value"],
		"second run's attribute data should be persisted independently")

	// The first row should still be readable via GetByRun (per-run
	// keying preserves history within a node).
	var firstByRun *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetByRun(h.Ctx, firstRunID, tx)
		firstByRun = r
		return err
	}))
	require.NotNil(t, firstByRun, "first run's attribute row should still be readable by run id")
	require.Equal(t, "first", firstByRun.Data["value"],
		"first run's data should be unchanged after the second run")
}

// adminInvalidate POSTs an admin invalidate against the given node.
func adminInvalidate(t *testing.T, h *scenario.Harness, instanceID, nodeID interface{ String() string }) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{})
	resp, err := http.Post(
		h.ControlBase+"/admin/instances/"+instanceID.String()+"/nodes/"+nodeID.String()+"/invalidate",
		"application/json", bytes.NewReader(body),
	)
	require.NoError(t, err)
	resp.Body.Close()
}
