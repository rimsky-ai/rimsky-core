// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package per_run_attributes

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

func TestPerRunAttributes_SequentialRunsTwoRows(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"value": "first"}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-two-runs", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/worker"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/worker", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"value"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-two-runs", map[string]any{})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)
	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	h.WaitForNodeState(w.ID, cascade.NodeStateFresh)
	var first *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
		first = r
		return err
	}))
	require.NotNil(t, first)
	firstRunID := first.NodeRunID
	require.Equal(t, "first", first.Data["value"])

	h.Stub.WhenType("worker").Success(map[string]any{"value": "second"}, true, "ok")

	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	deadline := time.Now().Add(15 * time.Second)
	var latest *persistence.NodeAttributesRow
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
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
