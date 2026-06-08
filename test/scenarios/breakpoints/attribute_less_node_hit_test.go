// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins the universality of the before_dispatch checkpoint across
// node shapes: a before_dispatch breakpoint MUST fire (and a pause-mode one
// MUST block) on a node that carries NO `attributes:` block, exactly as it
// does on an attribute-bearing node.
//
// Regression guard: the supervisor's before_dispatch breakpoint evaluation
// runs inside the pre-dispatch attribute-resolution pass. That pass used to
// return early for an attribute-less / schema-less node BEFORE reaching the
// checkpoint, so a breakpoint installed on a bare node silently never fired —
// an operator debugging such a node would wait forever for a hit that the
// supervisor walked straight past. The fix runs the checkpoint on every
// resolution exit path; this scenario proves both the notify_only (non-
// blocking, hit recorded) and pause (blocking, then resume) shapes on a bare
// node.
//
// @concept: breakpoint

package breakpoints

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestBreakpointFiresOnAttributeLessNode_NotifyOnly proves a notify_only
// before_dispatch breakpoint records a hit on a node carrying no attributes.
func TestBreakpointFiresOnAttributeLessNode_NotifyOnly(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, false, "ok")

	// A worker node with NO `attributes:` block — the degenerate shape whose
	// dispatch path returns early from attribute resolution.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-attrless-notify", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-attrless-notify", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
		"mode":       "notify_only",
	})
	_, _ = instanceResume(t, h, iid)

	// The supervisor must reach the checkpoint and record a hit even though
	// the node carries no attributes — the regression this scenario guards.
	hits := waitForHitCount(t, h, bpID, 1, 15*time.Second)
	require.Len(t, hits, 1)
	require.Equal(t, "before_dispatch", string(hits[0].Checkpoint))
	require.Equal(t, "notify_only", string(hits[0].Mode))

	// notify_only is non-blocking, so the dispatch still settles.
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"notify_only must not block the attribute-less dispatch — worker should reach Fresh")
}

// TestBreakpointFiresOnAttributeLessNode_PauseBlocks proves a pause-mode
// before_dispatch breakpoint on an attribute-less node BLOCKS the dispatch
// until resumed (the executor sees no dispatch while paused), then proceeds.
func TestBreakpointFiresOnAttributeLessNode_PauseBlocks(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, false, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-attrless-pause", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-attrless-pause", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status, "instance resume should succeed")

	// The hit proves the supervisor reached the checkpoint and is parked.
	hit := waitForHitOnBreakpoint(t, h, bpID, 15*time.Second)

	// While paused, the executor must NOT have seen the dispatch — the
	// before_dispatch checkpoint fired and blocked even on a bare node.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"pause-mode breakpoint must block the attribute-less dispatch before Execute")

	// Resume; the dispatch now proceeds to terminal.
	status, out := breakpointResume(t, h, iid, bpID, map[string]any{"hit_id": hit.ID.String()})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, out["resumed"])

	require.True(t, waitForStubObservedCount(h, "worker", 1, 15*time.Second),
		"the attribute-less worker must dispatch after the breakpoint resume")
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"the attribute-less worker should reach Fresh after resume")
}
