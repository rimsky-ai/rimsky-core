// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package per_run_attributes

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: attribute
func TestPerRunAttributes_FirstRunWritingExactDefaultFiresNoChangedSignal(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "first-run-default-baseline", Version: "1",
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
						"x": map[string]any{"type": "string", "default": "the-default"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-first-run-default-baseline", map[string]any{})
	aN := h.FindNode(iid, "a")
	require.NotNil(t, aN)

	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	h.WaitForNodeState(aN.ID, cascade.NodeStateFresh)

	require.False(t, h.HasEventKind(aN.ID, "attribute/x/changed"),
		"a first run that writes exactly its schema default must not fire attribute/x/changed")
}

// @concept: attribute
func TestPerRunAttributes_FirstRunDivergingFromDefaultFiresChangedSignal(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"x": "not-the-default"}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "first-run-default-baseline-diverges", Version: "1",
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
						"x": map[string]any{"type": "string", "default": "the-default"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-first-run-default-baseline-diverges", map[string]any{})
	aN := h.FindNode(iid, "a")
	require.NotNil(t, aN)

	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	h.WaitForEventCount(aN.ID, "attribute/x/changed", 1)
}
