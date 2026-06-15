// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Soft instance pause":
//
//   1. Start an instance. The first dispatch fires and reaches
//      terminal naturally.
//   2. Call POST /instances/{id}/pause — sets paused=true.
//   3. Send a message to provoke a follow-on dispatch. The candidate-
//      selection filter holds the new claim; no new dispatch fires.
//   4. Call POST /instances/{id}/resume → paused=false. The
//      supervisor's next tick claims the pending work and dispatches.
//
// Pins the §5.2 soft-pause semantics: in-flight runs settle naturally,
// new claims wait. The composition of pause + resume is idempotent on
// terminals already in-flight.
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

func TestSoftInstancePause(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-soft-pause", Version: "1",
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

	iid := h.CreateInstance(tid, "ck-soft-pause", map[string]any{})

	// @deliberate: Wait for the first dispatch's terminal so we know the harness
	// has a running supervisor + the in-flight dispatch settled.
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"initial dispatch should reach Fresh")
	startCount := stubObservedCount(h, "worker")
	require.GreaterOrEqual(t, startCount, 1,
		"first dispatch fired before pause")

	// @deliberate: Pause the instance. Subsequent claims should be held.
	status, _ := instancePause(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	// @deliberate: Provoke a re-dispatch by invalidating the worker node (a
	// node-level invalidate is the simplest way to force the supervisor
	// to enqueue another dispatch). The new dispatch should NOT fire
	// because the candidate-selection filter excludes paused instances.
	h.InvalidateNode(iid, n.ID)

	// @constraint: Give the supervisor a few ticks to (not) pick the row up.
	time.Sleep(1 * time.Second)
	heldCount := stubObservedCount(h, "worker")
	require.Equal(t, startCount, heldCount,
		"soft-pause should hold new claims; observed count must not advance while paused")

	// @deliberate: Resume the instance; the supervisor's next tick claims the
	// pending dispatch.
	status, _ = instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	require.True(t, waitForStubObservedCount(h, "worker", startCount+1, 10*time.Second),
		"second dispatch should fire after instance resume")
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"second dispatch should reach Fresh post-resume")
}
