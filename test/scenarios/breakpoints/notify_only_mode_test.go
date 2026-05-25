// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Notify-only mode":
//
//   1. Install a notify_only breakpoint.
//   2. The supervisor reaches the checkpoint, writes a hit row, and
//      continues (no waitForResume block).
//   3. The dispatch proceeds to terminal naturally.
//   4. The hit row remains, unresumed — the agent can read it on its
//      next poll, but the dispatch does not depend on the agent doing so.
//
// notify_only is the non-blocking observability shape; this scenario
// pins that it doesn't accidentally block the dispatch.
//
// @concept: breakpoint

package breakpoints

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

func TestNotifyOnlyMode(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-notify-only", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-notify-only", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
		"mode":       "notify_only",
	})
	_, _ = instanceResume(t, h, iid)

	// The dispatch should complete without an explicit resume call —
	// notify_only does not block. The terminal/success follows the
	// usual path; the hit row stays unresumed.
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"notify_only must not block dispatch — worker should reach Fresh without a resume call")

	// One hit row exists for the breakpoint, still unresumed.
	hits := waitForHitCount(t, h, bpID, 1, 5*time.Second)
	require.Len(t, hits, 1)
	require.Nil(t, hits[0].ResumedAt,
		"notify_only hit must NOT be auto-resumed; the agent reads it but does not need to release the dispatch")
	require.Equal(t, "notify_only", string(hits[0].Mode))
}
