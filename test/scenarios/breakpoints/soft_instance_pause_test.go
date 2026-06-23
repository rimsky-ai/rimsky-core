// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package breakpoints

import (
	"fmt"
	"net/http"
	"testing"
	"time"

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
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"initial dispatch should reach Fresh")
	startCount := stubObservedCount(h, "worker")
	require.GreaterOrEqual(t, startCount, 1,
		"first dispatch fired before pause")

	status, _ := instancePause(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	time.Sleep(1 * time.Second)
	heldCount := stubObservedCount(h, "worker")
	require.Equal(t, startCount, heldCount,
		"soft-pause should hold new claims; observed count must not advance while paused")

	status, _ = instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	require.True(t, waitForStubObservedCount(h, "worker", startCount+1, 10*time.Second),
		"second dispatch should fire after instance resume")
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"second dispatch should reach Fresh post-resume")
}
