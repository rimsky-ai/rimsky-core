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

// @story: upstream-pull-on-invalidate
func TestPerRunAttributes_HardDepPullsUpstream(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-1"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-hard-dep", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/a"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/a", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"a_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b_value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"b_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "a", Type: "attribute/a_value/changed", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "b", Type: "attribute/b_value/changed", ForceUpstreamRefresh: node.BoolPtr(true)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.a_value}}",
						},
						"b_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.b.attribute.b_value}}",
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
	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	h.WaitForNodeState(cN.ID, cascade.NodeStateFresh)

	var cRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
		cRow = r
		return err
	}))
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-1", cRow.Data["a_val"], "c should see a's first-fire value")
	require.Equal(t, "from-b-1", cRow.Data["b_val"], "c should see b's first-fire value (upstream-refresh pulled)")

	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-2"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
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
		"c should see b's second-fire value (upstream-refresh cascade re-fired b)")
}

// @story: upstream-pull-on-invalidate
func TestPerRunAttributes_HardDepPullsUpstream_DirectInvalidateOfReceiver(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-1"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-hard-dep-direct", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/c"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"a_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b_value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"b_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "a", Type: "attribute/a_value/changed", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "b", Type: "attribute/b_value/changed", ForceUpstreamRefresh: node.BoolPtr(true)},
					node.SubscriptionEntry{Node: "test/wake/c", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.a_value?}}",
						},
						"b_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.b.attribute.b_value}}",
						},
					},
					"required": []any{"a_val", "b_val"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-hard-dep-direct", map[string]any{})
	aN := h.FindNode(iid, "a")
	bN := h.FindNode(iid, "b")
	cN := h.FindNode(iid, "c")
	require.NotNil(t, aN)
	require.NotNil(t, bN)
	require.NotNil(t, cN)

	h.WaitForNodeState(cN.ID, cascade.NodeStateFresh)

	var cRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
		cRow = r
		return err
	}))
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-1", cRow.Data["a_val"], "c should see a's first-fire value")
	require.Equal(t, "from-b-1", cRow.Data["b_val"], "c should see b's first-fire value")

	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	h.PostInstanceMessage(iid, "test/wake/c", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	bDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(bDeadline) {
		var bRow *persistence.NodeAttributesRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, bN.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
			bRow = r
			return err
		})
		if bRow != nil && bRow.Data["b_value"] == "from-b-2" {
			t.Logf("b re-ran successfully with from-b-2")
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
			cRow = r
			return err
		})
		if cRow != nil && cRow.Data["b_val"] == "from-b-2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, cRow)
	require.Equal(t, "from-b-2", cRow.Data["b_val"],
		"c's direct invalidate must pull b (force_upstream_refresh: true) into the frame; "+
			"b's second-fire value is the load-bearing observable for the upstream-refresh "+
			"pull pass driven by pullUpstreamRefreshesForNode")
}
