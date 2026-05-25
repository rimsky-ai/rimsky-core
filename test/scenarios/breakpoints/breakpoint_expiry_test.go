// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Breakpoint expiry":
//
//   - Install a breakpoint with ttl_seconds = 1.
//   - Wait past t=2s so the scheduler's sweep tick deletes the
//     breakpoint row.
//   - Subsequent matching dispatches must NOT hit the breakpoint —
//     the matcher set is empty after expiry.
//
// Pins both the §4.8 SweepExpired contract (TTL-bounded breakpoint
// auto-deletion) and the supervisor's evaluator's tolerance of
// "no breakpoints for this instance" (the absence of a matched row is
// the only correctness signal).
//
// @concept: breakpoint

package breakpoints

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestBreakpointExpiry(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		// Fast tick so SweepExpired fires soon after the TTL elapses.
		SchedulerTick: 100 * time.Millisecond,
	})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-breakpoint-expiry", Version: "1",
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

	iid := createInstanceWithPause(t, h, tid, "ck-breakpoint-expiry", map[string]any{})
	ttl := 1
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint":  "before_dispatch",
		"matcher":     map[string]any{"node_type": "worker"},
		"mode":        "pause",
		"ttl_seconds": ttl,
	})
	// The breakpoint exists right now.
	require.NotNil(t, getBreakpointRow(t, h, bpID),
		"breakpoint should exist immediately after creation")

	// Wait past the TTL + a sweep tick or two. The sweep is scheduled
	// every 100ms; SweepExpired uses NOW() > expires_at.
	require.Eventually(t, func() bool {
		// Use includeExpired=true via direct row lookup; SweepExpired
		// physically DELETEs the row, so Get returns nil once it fires.
		return getBreakpointRow(t, h, bpID) == nil
	}, 5*time.Second, 100*time.Millisecond,
		"breakpoint row should be deleted by SweepExpired within TTL + sweep cadence")

	// Resume the instance — the worker should dispatch unimpeded since
	// the breakpoint is gone.
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach Fresh after the breakpoint expired (no pause)")

	// And no hit row should have landed — the breakpoint was already
	// gone by dispatch time. Probe the hit table via the InstanceID
	// scan; an expired breakpoint deletes hits via FK CASCADE, so 0
	// rows is the assertion.
	var hits []persistence.BreakpointHitRow
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.BreakpointHits().ListSinceForInstance(ctx, iid, 0, 100, tx)
		hits = r
		return err
	}))
	require.Empty(t, hits,
		"no hit rows should remain after expiry — dispatch ran post-deletion and FK CASCADE removed any prior hits")
}
