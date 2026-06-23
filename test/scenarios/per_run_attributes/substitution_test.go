// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestPerRunAttributes_DownstreamReadsThisFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("upstream").Success(map[string]any{"value": "fire-1"}, true, "ok")
	h.Stub.WhenType("downstream").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-downstream-read", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/upstream"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "upstream", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/upstream", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string"},
					},
					"required": []any{"value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "upstream", Type: "attribute/value/changed", ForceUpstreamRefresh: node.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"upstream_value": map[string]any{
							"type":   "string",
							"source": "{{nodes.upstream.attribute.value}}",
						},
					},
					"required": []any{"upstream_value"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-this-frame", map[string]any{})
	upN := h.FindNode(iid, "upstream")
	downN := h.FindNode(iid, "downstream")
	require.NotNil(t, upN)
	require.NotNil(t, downN)
	h.PostInstanceMessage(iid, "test/wake/upstream", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	require.True(t, h.WaitForNodeState(upN.ID, cascade.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(downN.ID, cascade.NodeStateFresh, 15*time.Second))

	var downRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, downN.ID, h.GetMainRunScopeID(iid), tx)
		downRow = r
		return err
	}))
	require.NotNil(t, downRow)
	require.Equal(t, "fire-1", downRow.Data["upstream_value"],
		"downstream should see fire-1 upstream value")

	h.Stub.WhenType("upstream").Success(map[string]any{"value": "fire-2"}, true, "ok")
	h.Stub.WhenType("downstream").Success(map[string]any{}, true, "ok")

	h.PostInstanceMessage(iid, "test/wake/upstream", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, downN.ID, h.GetMainRunScopeID(iid), tx)
			downRow = r
			return err
		})
		if downRow != nil && downRow.Data["upstream_value"] == "fire-2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, downRow)
	require.Equal(t, "fire-2", downRow.Data["upstream_value"],
		"downstream should see fire-2 upstream value (this-frame, not stale)")
}
