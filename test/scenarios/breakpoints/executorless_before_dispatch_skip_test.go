// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package breakpoints

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestBreakpointBeforeDispatchSkippedForExecutorlessNode(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-executorless-skip", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/hub"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "hub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/hub", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-bp-executorless-skip", map[string]any{})
	hub := h.FindNode(iid, "hub")
	require.NotNil(t, hub, "hub node row must exist")
	require.Empty(t, hub.Executor, "hub must be a pure-cascade (executor-less) node for this guard to be meaningful")

	beforeBP := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "hub"},
		"mode":       "notify_only",
	})
	afterBP := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "after_terminal",
		"matcher":    map[string]any{"node_type": "hub"},
		"mode":       "notify_only",
	})

	h.PostInstanceMessage(iid, "test/wake/hub", nil, fmt.Sprintf("test-wake-%s", t.Name()))

	waitForHitCount(t, h, afterBP, 1)
	h.WaitForNodeState(hub.ID, cascade.NodeStateFresh)

	beforeHits := listHitsForBreakpoint(t, h, beforeBP)
	require.Lenf(t, beforeHits, 0,
		"before_dispatch is executor-scoped (runner.go gates resolveAttributes on acq.Executor != \"\"); "+
			"an executor-less pure-cascade dispatch must never reach evaluateBeforeDispatchBreakpoints even though "+
			"after_terminal fires unconditionally (observed above); got %d before_dispatch hits", len(beforeHits))
}
