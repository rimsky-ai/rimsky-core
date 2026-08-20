// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint

package breakpoints

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSoftInstancePause(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-soft-pause", Version: "1",
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
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-soft-pause", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
	startCount := stubObservedCount(h, "worker")
	require.GreaterOrEqual(t, startCount, 1,
		"first dispatch fired before pause")

	status, _ := instancePause(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	h.WaitForSchedulerQuiescence()
	heldCount := stubObservedCount(h, "worker")
	require.Equal(t, startCount, heldCount,
		"soft-pause should hold new claims; observed count must not advance while paused")

	status, _ = instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	waitForStubObservedCount(h, "worker", startCount+1)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)
}
